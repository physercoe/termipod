package server

import (
	"encoding/json"
	"strings"
)

// Sniffing a run's dataset out of its logged config (plan §8, wedge W5).
//
// A training run's config names the dataset it trained on; an eval run's names
// what it rolled out against. Reading it turns "link this run to a dataset"
// from a search into a one-click confirmation.
//
// Everything here only ever PROPOSES. The write is a separate, explicit act,
// because a config key that merely looks like a dataset is a guess, and a wrong
// edge sends someone to watch the wrong robot and believe what they see.
//
// The key names are LeRobot's, read off the config classes rather than
// remembered: `TrainPipelineConfig.dataset` is a `DatasetConfig` with
// `repo_id: str` and `root: str | None` (src/lerobot/configs/train.py,
// configs/default.py). Loggers flatten nested config differently — trackio and
// wandb both write dotted keys, some launchers write underscored ones — so both
// spellings are accepted for the same field.

// DatasetHint is a proposed dataset for a run, and where it came from.
type DatasetHint struct {
	// The location or repo id read out of the config.
	Value string `json:"value"`
	// "path" when Value is a filesystem root, "repo_id" when it is an
	// org/name handle. They match a dataset row differently.
	Kind string `json:"kind"`
	// The config key it was read from, so a wrong proposal is debuggable
	// rather than mysterious.
	Key string `json:"key"`
}

// hintKey is one config field worth reading, in priority order.
type hintKey struct {
	// Dotted path into the (possibly nested) config.
	path string
	kind string
	// Alternative flat spellings of the same field.
	aliases []string
}

// Ordered by how well the field identifies a dataset ROW. A root path is a
// location, which is exactly what `datasets` is keyed by; a repo id has to be
// matched against a path suffix or a name, which is weaker.
var datasetHintKeys = []hintKey{
	{path: "dataset.root", kind: "path", aliases: []string{"dataset_root"}},
	{path: "dataset.repo_id", kind: "repo_id", aliases: []string{"dataset_repo_id", "dataset.repo-id"}},
	// Where an eval run writes its rollouts. Distinct from the dataset it
	// evaluates against, and the reason W5 can offer "watch what this eval
	// actually did" at all.
	{path: "eval.recording_repo_id", kind: "repo_id", aliases: []string{"eval_recording_repo_id"}},
}

// There is deliberately no deny list here, and `repo_id` alone is deliberately
// not a key.
//
// `policy.repo_id` is the MODEL — it sits one key away from the dataset in
// every training config, and reading it would link every training run to a
// dataset that does not exist. What keeps it out is that every lookup above is
// a fully-qualified path: nothing ever matches a bare trailing name, so
// `policy.repo_id` is unreachable rather than merely rejected. A deny list was
// written first and then removed once it turned out to be unreachable too —
// dead defensive code reads as a safety net and would be trusted by whoever
// later adds a bare key. `TestDatasetHintIgnoresThePolicyRepoID` pins the
// behaviour however it is achieved.

// datasetHintFromConfig reads the first recognised dataset field out of a
// logged run config. Returns nil when the config names none — the common case,
// since most runs are not about a dataset at all.
func datasetHintFromConfig(raw []byte) *DatasetHint {
	if len(raw) == 0 {
		return nil
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return nil
	}
	for _, k := range datasetHintKeys {
		for _, candidate := range append([]string{k.path}, k.aliases...) {
			v, key := lookupConfigValue(obj, candidate)
			if v == "" {
				continue
			}
			return &DatasetHint{Value: v, Kind: k.kind, Key: key}
		}
	}
	return nil
}

// lookupConfigValue resolves a dotted path against a config that may be nested
// OR flattened, and returns the value with the key it was actually found under.
//
// Both shapes are live: a config PUT straight from a dataclass dump is nested,
// while trackio and wandb flatten to dotted keys. Reading only one shape means
// half the loggers silently produce no hint at all.
func lookupConfigValue(obj map[string]any, path string) (string, string) {
	if v, ok := obj[path]; ok {
		return scalarString(v), path
	}
	// Case-insensitive flat match, since launchers differ on capitalisation.
	for k, v := range obj {
		if strings.EqualFold(k, path) {
			return scalarString(v), k
		}
	}
	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return "", ""
	}
	cur := obj
	for i := 0; i < len(parts)-1; i++ {
		next, ok := cur[parts[i]]
		if !ok {
			for k, v := range cur {
				if strings.EqualFold(k, parts[i]) {
					next, ok = v, true
					break
				}
			}
		}
		if !ok {
			return "", ""
		}
		m, ok := next.(map[string]any)
		if !ok {
			return "", ""
		}
		cur = m
	}
	last := parts[len(parts)-1]
	if v, ok := cur[last]; ok {
		return scalarString(v), path
	}
	for k, v := range cur {
		if strings.EqualFold(k, last) {
			return scalarString(v), strings.Join(parts[:len(parts)-1], ".") + "." + k
		}
	}
	return "", ""
}

// scalarString accepts a string, or the FIRST element of a list of strings.
//
// `dataset.repo_id` is legitimately a list — LeRobot trains on several datasets
// at once, and its own config validation branches on `isinstance(..., list)`.
// One run then has several datasets, which the single `runs.dataset_id` column
// cannot express; proposing the first is honest as a starting point, and the
// user confirms or replaces it.
func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// datasetMatchesHint reports whether a registered dataset is the one a hint
// names.
//
// A `path` hint matches the root exactly, modulo trailing separators — the same
// rule the Inspect handoff uses, because the hub stores the root string as sent.
// A `repo_id` hint matches the dataset's NAME, or a root whose last two segments
// are the repo id: `$HF_LEROBOT_HOME/lerobot/pusht` is where the cache puts
// `lerobot/pusht`, and matching the whole path would never fire while matching
// only the last segment would confuse `alice/pusht` with `bob/pusht`.
func datasetMatchesHint(hint DatasetHint, rootPath, name string) bool {
	if hint.Value == "" {
		return false
	}
	if hint.Kind == "path" {
		return trimTrailingSeparators(rootPath) == trimTrailingSeparators(hint.Value)
	}
	if strings.EqualFold(strings.TrimSpace(name), hint.Value) {
		return true
	}
	repo := strings.Trim(strings.ReplaceAll(hint.Value, "\\", "/"), "/")
	if repo == "" {
		return false
	}
	root := strings.Trim(strings.ReplaceAll(trimTrailingSeparators(rootPath), "\\", "/"), "/")
	return root == repo || strings.HasSuffix(root, "/"+repo)
}

func trimTrailingSeparators(p string) string {
	return strings.TrimRight(p, "/\\")
}
