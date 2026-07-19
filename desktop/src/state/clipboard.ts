import { toast } from './toast';

/// Copy a secret to the clipboard and schedule an automatic clear, so a password
/// doesn't sit in the OS clipboard indefinitely where the next paste (or another
/// app's clipboard reader) could leak it (#320). Mirrors 1Password/Bitwarden's
/// "clear clipboard after N seconds". The clear only fires if the clipboard still
/// holds what we wrote — if the user copied something else meanwhile, we leave it.

const CLEAR_AFTER_MS = 45_000;
let clearTimer: ReturnType<typeof setTimeout> | undefined;

export async function copySecret(value: string, opts?: { announce?: boolean }): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(value);
  } catch {
    return false;
  }
  if (clearTimer !== undefined) clearTimeout(clearTimer);
  clearTimer = setTimeout(() => {
    void (async () => {
      try {
        // Only wipe if it's still our secret — never stomp a later copy.
        const current = await navigator.clipboard.readText();
        if (current === value) await navigator.clipboard.writeText('');
      } catch {
        /* clipboard read/write blocked — best effort */
      }
    })();
  }, CLEAR_AFTER_MS);
  if (opts?.announce === true) toast.info('Copied — clipboard clears in 45s');
  return true;
}
