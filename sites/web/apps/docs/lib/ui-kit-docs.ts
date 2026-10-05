import { uiKitCatalog } from '@kb-labs/sdk/studio/catalog';

export function uiKitComponentSlug(name: string): string {
  return name.replace(/^UI/, '').replace(/([a-z0-9])([A-Z])/g, '$1-$2').toLowerCase();
}

export function findUiKitComponent(slug: string[] | undefined) {
  if (!slug || slug.length !== 4 || slug[0] !== 'sdk' || slug[1] !== 'plugin-api') return undefined;
  const category = slug[2].replace(/^studio-/, '');
  if (category === slug[2]) return undefined;
  return uiKitCatalog.find((component) => component.category === category && uiKitComponentSlug(component.name) === slug[3]);
}
