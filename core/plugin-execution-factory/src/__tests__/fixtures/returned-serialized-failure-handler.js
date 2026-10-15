const returnedStack = 'Error: serialized returned failure\n    at registeredHandler (handler.js:7:3)';

export default {
  async execute() {
    return {
      ok: false,
      error: {
        name: 'Error',
        message: 'returned handler failure',
        stack: returnedStack,
      },
    };
  },
};
