import { useEffect, useRef } from 'react';
import { Crepe } from '@milkdown/crepe';
import '@milkdown/crepe/theme/common/style.css';
import '@milkdown/crepe/theme/frame.css';
import { loadNoteImage, NOTE_ATT_SCHEME, writeNoteImage } from '../state/attachments';

/// A WYSIWYG Markdown editor built on Milkdown's Crepe (ProseMirror + remark).
/// Unlike TinyMCE it edits *Markdown* — the on-disk/hub format is unchanged, so
/// it stays interoperable with the raw source editor (MarkdownEditor), the
/// `<Markdown>` renderer, the assistant context, and `.md` export.
///
/// Images use our de-inlined attachment scheme natively via Crepe's ImageBlock:
///   • onUpload — a pasted/dropped image is written as a managed attachment and
///     referenced as `termipod-att://<key>/<file>` (never base64 in the note).
///   • proxyDomURL — that ref resolves to a blob object URL for the `<img>` in
///     the DOM, while the stored markdown keeps the portable ref.
/// So the Markdown that goes in and comes out carries the same refs as Layers
/// 1–2; nothing is translated at the boundary.
///
/// Uncontrolled after mount (feeding `value` back would fight the cursor): the
/// parent remounts with `key` to load a different note/document; edits flow out
/// through `onChange` (Crepe's markdownUpdated listener).

// Chunked base64 so a multi-MB image doesn't blow the argument limit of btoa.
function bytesToB64(bytes: Uint8Array): string {
  let bin = '';
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    bin += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(bin);
}

export function WysiwygEditor({
  value,
  onChange,
  placeholder,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}): JSX.Element {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  useEffect(() => {
    const host = hostRef.current;
    if (host === null) return;
    const objectUrls: string[] = [];
    let crepe: Crepe | null = null;
    let destroyed = false;

    const uploadImage = async (file: File): Promise<string> => {
      const buf = await file.arrayBuffer();
      const b64 = bytesToB64(new Uint8Array(buf));
      const ref = await writeNoteImage(b64, file.name !== '' ? file.name : 'image.png');
      // Browser build (no file access) falls back to an inline data-URI.
      return ref ?? `data:${file.type !== '' ? file.type : 'image/png'};base64,${b64}`;
    };

    const proxyUrl = async (url: string): Promise<string> => {
      if (!url.startsWith(NOTE_ATT_SCHEME)) return url;
      const blob = await loadNoteImage(url);
      if (blob === null) return url;
      const obj = URL.createObjectURL(blob);
      objectUrls.push(obj);
      return obj;
    };

    const build = async (): Promise<void> => {
      const c = new Crepe({
        root: host,
        defaultValue: value,
        features: {
          [Crepe.Feature.CodeMirror]: true,
          [Crepe.Feature.ListItem]: true,
          [Crepe.Feature.LinkTooltip]: true,
          [Crepe.Feature.Cursor]: true,
          [Crepe.Feature.ImageBlock]: true,
          [Crepe.Feature.BlockEdit]: true,
          [Crepe.Feature.Toolbar]: true,
          [Crepe.Feature.Placeholder]: true,
          [Crepe.Feature.Table]: true,
          [Crepe.Feature.Latex]: true,
        },
        featureConfigs: {
          [Crepe.Feature.Placeholder]: { text: placeholder ?? '', mode: 'doc' },
          [Crepe.Feature.ImageBlock]: {
            onUpload: uploadImage,
            blockOnUpload: uploadImage,
            inlineOnUpload: uploadImage,
            proxyDomURL: proxyUrl,
          },
        },
      });
      c.on((api) => {
        api.markdownUpdated((_ctx, markdown) => onChangeRef.current(markdown));
      });
      await c.create();
      if (destroyed) {
        void c.destroy();
        return;
      }
      crepe = c;
    };
    void build();

    return () => {
      destroyed = true;
      if (crepe !== null) void crepe.destroy();
      objectUrls.forEach((u) => URL.revokeObjectURL(u));
    };
    // Create once; the parent remounts via key to swap documents.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return <div className="milkdown-host" ref={hostRef} />;
}
