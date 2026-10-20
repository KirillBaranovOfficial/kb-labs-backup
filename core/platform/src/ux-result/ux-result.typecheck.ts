import { createUxSuccessResult } from './index.js';

createUxSuccessResult(null);
// @ts-expect-error A success envelope must contain JSON-serializable data.
createUxSuccessResult(undefined);
