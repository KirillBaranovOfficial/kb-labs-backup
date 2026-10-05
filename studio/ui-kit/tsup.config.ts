import { defineConfig } from 'tsup';
import reactLibPreset from '@kb-labs/devkit/tsup/react-lib.js';

export default defineConfig({
  ...reactLibPreset,
  entry: {
    index: 'src/index.ts',
    catalog: 'src/catalog.generated.ts',
  },
  tsconfig: "tsconfig.build.json",
});
