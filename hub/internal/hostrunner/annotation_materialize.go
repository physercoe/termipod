// annotation_materialize.go — D5's fallback for image-less drivers
// (docs/plans/desktop-ui-context-and-pointing.md §3.5).
//
// The annotation overlay hands an image to the agent, and for the ACP
// and stream-json families that is native multimodal input. Three
// families cannot take it:
//
//   - the PANE driver (M4 tmux): the pane is a terminal. There is no
//     remote image channel — the kimi TUI's clipboard paste is a LOCAL
//     feature, not something tmux send-keys can drive;
//   - gemini's exec-per-turn argv (`gemini -p "<text>"`): no inline
//     image affordance at all;
//   - an ACP agent that declares promptCapabilities.image == false.
//
// Until now all three DROPPED the image and (for two of them) posted a
// warning. Dropping is the wrong answer for an annotation: the user
// deliberately pointed at something, and every one of these agents can
// read a file. So the image is materialized into the agent's own
// workdir and the path is appended to the note text — "see the
// attached image at <path>" — which the agent reads with its normal
// file tools.
//
// Two rules the plan states and this file enforces:
//   - it lands under `<workdir>/.termipod/annotations/`, a directory
//     the agent already has, so no new permission surface appears;
//   - "if the workdir write fails, the driver notes the drop rather
//     than failing silently" — the caller keeps its existing warning
//     event, and this function never fails the turn.

package hostrunner

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// decodeBase64Image tolerates the whitespace-bearing base64 the MCP
// attach path is known to produce (see the attach cap notes) — a
// forwarded payload that survived hub validation must not fail here on
// a newline.
func decodeBase64Image(data string) ([]byte, error) {
	clean := strings.NewReplacer("\n", "", "\r", "", " ", "", "\t", "").Replace(data)
	raw, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("not base64: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty image")
	}
	return raw, nil
}

// annotationDirName is the per-workdir home for materialized inputs.
// Under `.termipod/` because that prefix is already the agent-local
// scratch convention, and a dotted dir keeps it out of the way of the
// agent's actual work.
const annotationDirName = ".termipod/annotations"

// annotationExt maps the mime types the hub accepts onto a file
// extension. An unknown mime gets `.bin`: the note names the path, and
// a wrong extension is a worse lie than a generic one.
func annotationExt(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".bin"
	}
}

// annotationFilename is the stamped name for one materialized image.
// Deterministic given (stamp, index) so a test can assert it, and
// sortable so a workdir's annotations read chronologically.
func annotationFilename(stamp time.Time, index int, mime string) string {
	return fmt.Sprintf("%s-%d%s", stamp.UTC().Format("20060102T150405Z"), index, annotationExt(mime))
}

// materializeImageInputs writes each image into
// `<workdir>/.termipod/annotations/` and returns the paths written.
//
// Errors are returned but are NOT fatal to a turn: the caller reports
// the drop (its existing warning event) and sends the text alone. A
// partial success returns the paths that landed plus the error, so the
// note can still name what the agent can actually open.
func materializeImageInputs(workdir string, images []imageInput, stamp time.Time) ([]string, error) {
	if len(images) == 0 {
		return nil, nil
	}
	if workdir == "" {
		return nil, fmt.Errorf("no workdir: cannot materialize %d image(s)", len(images))
	}
	dir := filepath.Join(workdir, filepath.FromSlash(annotationDirName))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	paths := make([]string, 0, len(images))
	for i, img := range images {
		raw, err := decodeBase64Image(img.data)
		if err != nil {
			return paths, fmt.Errorf("image %d: %w", i+1, err)
		}
		p := filepath.Join(dir, annotationFilename(stamp, i+1, img.mime))
		// 0o600: the crop is a frame of the user's screen. The agent
		// runs as the same user, so this is enough to keep it off other
		// accounts on a shared host.
		if err := os.WriteFile(p, raw, 0o600); err != nil {
			return paths, fmt.Errorf("write %s: %w", p, err)
		}
		paths = append(paths, p)
	}
	pruneAnnotationDir(dir)
	return paths, nil
}

// annotationKeep caps how many materialized images loiter per workdir.
// A crop is a one-turn artifact, not a gallery — without a cap the
// directory grows for the workdir's lifetime. Newest-kept, because a
// path named in a recent note should keep resolving; the filenames are
// UTC-stamped so lexical order IS chronological order.
const annotationKeep = 32

// pruneAnnotationDir drops the oldest materialized images beyond the
// cap. Best-effort by design: a prune failure must never fail the turn
// that just materialized successfully.
func pruneAnnotationDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	if len(names) <= annotationKeep {
		return
	}
	sort.Strings(names)
	for _, name := range names[:len(names)-annotationKeep] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// fallbackReason renders the materialization failure for the warning
// event. "the driver notes the drop rather than failing silently"
// (plan §3.5) — the principal needs to know WHY their annotation did
// not reach the agent, not just that it didn't.
func fallbackReason(err error) string {
	if err == nil {
		return "partial write"
	}
	return err.Error()
}

// annotationNote appends the materialized paths to the user's text so
// the agent knows the image exists and where to open it. The user's
// own words come first — they are the message.
func annotationNote(body string, paths []string) string {
	if len(paths) == 0 {
		return body
	}
	var b strings.Builder
	if body != "" {
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	if len(paths) == 1 {
		b.WriteString("[attached image saved at " + paths[0] + " — open it with your file tools]")
		return b.String()
	}
	b.WriteString("[attached images saved — open them with your file tools]")
	for _, p := range paths {
		b.WriteString("\n- " + p)
	}
	return b.String()
}
