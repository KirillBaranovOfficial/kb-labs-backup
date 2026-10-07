let activeRequestId;

process.send({ type: 'ready', pid: process.pid });

process.on('message', (message) => {
  if (message.type === 'health') {
    process.send({ type: 'healthOk' });
    return;
  }
  if (message.type !== 'execute') return;
  activeRequestId = message.requestId;

  process.send({
    type: 'uiPrompt',
    requestId: message.requestId,
    promptId: 'prompt-worker-test',
    kind: 'confirm',
    message: 'Continue?',
    defaultValue: true,
  });
});

process.on('message', (message) => {
  if (message.type !== 'uiPromptResult') return;
  if (message.error) {
    process.send({
      type: 'error',
      requestId: activeRequestId,
      error: {
        message: message.error.message,
        code: message.error.code,
        hint: message.error.hint,
        cause: message.error.cause,
        details: message.error.details,
        cataloguedError: message.error,
      },
    });
    return;
  }
  process.send({
    type: 'result',
    requestId: activeRequestId,
    result: { confirmed: message.value },
  });
});
