/// A non-2xx hub response. `status` mirrors HubApiError.status in the Dart client;
/// teamGate scope mismatches surface here as 403.
export class HubApiError extends Error {
  readonly status: number;
  constructor(status: number, message: string) {
    super(`HubApiError(${status}): ${message}`);
    this.name = 'HubApiError';
    this.status = status;
  }
}
