process.send({ type: 'ready', pid: process.pid });

process.on('message', (message) => {
  if (message.type === 'execute') {
    process.exit(23);
  }
  if (message.type === 'health') {
    process.send({ type: 'healthOk' });
  }
  if (message.type === 'shutdown') {
    process.exit(0);
  }
});
