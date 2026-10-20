export default {
  async execute() {
    return {
      ok: false,
      error: {
        code: 'COMMIT_COMMAND_FAILED',
        area: 'plugin',
        stage: 'run',
        severity: 'error',
        retryable: false,
        message: 'Commit command failed.',
        hint: null,
      },
    };
  },
};
