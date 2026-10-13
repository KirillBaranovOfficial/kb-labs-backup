export const isUniqueIndexConflict = (error: unknown): boolean => {
  if (typeof error !== 'object' || error === null) { return false; }
  if ('code' in error && error.code === 11000) { return true; }
  return error instanceof Error
    && /unique index violation|UNIQUE constraint failed/i.test(error.message);
};
