import { loadJson, saveJson, secretDelete, secretGet, secretSet } from '../state/persist';

/// Voice (DashScope ASR) settings. The API key is a secret → OS keychain; the
/// model choice is plain metadata → localStorage. Mirrors the mobile
/// voice_settings_provider (secure key + model), minus the region toggle (we
/// default to the primary DashScope endpoint, resolved Rust-side).
const KEY_SECRET = 'voice_dashscope_api_key';
const MODEL_KEY = 'voice.model';

export const VOICE_MODELS = ['paraformer-realtime-v2', 'fun-asr-realtime'];

export function getVoiceModel(): string {
  return loadJson<string>(MODEL_KEY, VOICE_MODELS[0]);
}
export function setVoiceModel(model: string): void {
  saveJson(MODEL_KEY, model);
}
export function getVoiceApiKey(): Promise<string | null> {
  return secretGet(KEY_SECRET);
}
export function setVoiceApiKey(key: string): Promise<void> {
  return key.trim() === '' ? secretDelete(KEY_SECRET) : secretSet(KEY_SECRET, key.trim());
}
