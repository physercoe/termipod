import { createContext, useContext } from 'react';
import { openExternal } from '../platform';

/// How a clicked external link should be opened. The Read surface provides an
/// in-app browser-tab opener (director request: links open in a dedicated tab
/// inside the app, not the OS browser); everywhere else there's no in-app
/// browser, so the default hands the URL to the OS browser via `openExternal`.
export const OpenLinkContext = createContext<((url: string) => void) | null>(null);

export function useOpenLink(): (url: string) => void {
  return useContext(OpenLinkContext) ?? openExternal;
}
