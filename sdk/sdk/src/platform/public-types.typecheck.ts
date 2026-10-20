import type { UxResult } from './index.js';

declare const result: UxResult<{ id: string }>;

if (result.ok) {
  const id: string = result.data.id;
  void id;
}
