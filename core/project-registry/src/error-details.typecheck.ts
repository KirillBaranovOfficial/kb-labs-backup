import { ProjectRegistryError } from './errors.js';

new ProjectRegistryError('KB_PROJECT_UNKNOWN', 'not registered', { path: '/tmp/project' });

// @ts-expect-error Project registry details must match the selected catalogue code.
new ProjectRegistryError('KB_PROJECT_UNKNOWN', 'not registered', { field: 'project' });
