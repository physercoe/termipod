/// Author document open/save (ADR-055 M1.4) — port of `src-tauri/src/docfile.rs`.
/// Same command names + shapes: `doc_open` (dialog), `doc_read` (by path),
/// `doc_save` (save dialog), `doc_write` (re-save by path).
import { writeFile } from 'node:fs/promises';
import type { Ctx, Handler } from './dispatch';
import { openDialog, saveDialog } from './dialogs';
import { readTextStrict } from './fsutil';

interface OpenedDoc {
  path: string;
  content: string;
}

// Openable Author documents. Keep in sync with AuthorNav's TEXT_EXT (the
// workspace-tree gate): figure sources (mmd/dot/gv/nomnoml; the *.json specs
// ride on 'json') and Phase C sketch scenes ('excalidraw').
const TEXT_EXTS = ['md', 'markdown', 'txt', 'drawio', 'xml', 'svg', 'canvas', 'json', 'csv', 'mmd', 'dot', 'gv', 'nomnoml', 'excalidraw'];

export const docfileHandlers: Record<string, Handler> = {
  doc_open: async (_args, ctx: Ctx): Promise<OpenedDoc | null> => {
    const res = await openDialog(ctx.win, {
      properties: ['openFile'],
      filters: [{ name: 'Documents', extensions: TEXT_EXTS }],
    });
    if (res.canceled || res.filePaths.length === 0) return null;
    const p = res.filePaths[0];
    return { path: p, content: await readTextStrict(p) };
  },

  doc_read: async (args): Promise<OpenedDoc> => {
    const p = String(args.path ?? '');
    return { path: p, content: await readTextStrict(p) };
  },

  // Inspect (J3) file open — same OpenedDoc shape as doc_open but with
  // code/diff/log/model filters instead of the document ones. A model checkpoint
  // is binary (its parser is W4's header-only main-process reader, not a UTF-8
  // slurp), so a failed strict read degrades to empty content rather than
  // throwing — the renderer sniffs the kind from the extension and shows the
  // right viewer/placard regardless.
  debug_open: async (_args, ctx: Ctx): Promise<OpenedDoc | null> => {
    const res = await openDialog(ctx.win, {
      properties: ['openFile'],
      filters: [
        {
          name: 'Code',
          extensions: [
            'py', 'js', 'ts', 'tsx', 'jsx', 'go', 'rs', 'c', 'h', 'cc', 'cpp', 'hpp', 'java', 'kt', 'rb', 'php', 'swift',
            'sh', 'bash', 'zsh', 'json', 'yaml', 'yml', 'toml', 'md', 'txt', 'sql', 'css', 'scss', 'html', 'xml',
          ],
        },
        { name: 'Diffs', extensions: ['diff', 'patch'] },
        { name: 'Logs', extensions: ['log'] },
        { name: 'Graphs', extensions: ['dot', 'gv'] },
        { name: 'Models', extensions: ['safetensors', 'gguf', 'onnx'] },
        { name: 'All files', extensions: ['*'] },
      ],
    });
    if (res.canceled || res.filePaths.length === 0) return null;
    const p = res.filePaths[0];
    try {
      return { path: p, content: await readTextStrict(p) };
    } catch {
      return { path: p, content: '' };
    }
  },

  doc_save: async (args, ctx: Ctx): Promise<string | null> => {
    const content = String(args.content ?? '');
    const defaultName = String(args.defaultName ?? '');
    const res = await saveDialog(ctx.win, { defaultPath: defaultName });
    if (res.canceled || res.filePath === undefined || res.filePath === '') return null;
    await writeFile(res.filePath, content, 'utf8');
    return res.filePath;
  },

  doc_write: async (args): Promise<void> => {
    await writeFile(String(args.path ?? ''), String(args.content ?? ''), 'utf8');
  },
};
