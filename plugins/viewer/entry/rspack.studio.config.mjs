import { createStudioRemoteConfig } from '@kb-labs/studio-plugin-tools';

export default await createStudioRemoteConfig({
  name: 'viewerPlugin',
  exposes: { './ViewerPage': './src/studio/pages/ViewerPage.tsx' },
});
