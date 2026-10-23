export const manifest = {
  schema: 'kb.adapter/1',
  id: 'fixture-adapter',
  name: 'Update Compatibility Fixture Adapter',
  version: '1.0.0',
  description: 'Journey adapter whose package identity is used by update compatibility checks',
  implements: 'IStorage',
  compatibility: { platformRange: '<3.2.0' },
};
