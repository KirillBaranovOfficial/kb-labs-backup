/** Shared size scale for Studio UI kit components. */
export type UISize = 'sm' | 'md' | 'lg';

/** Legacy Ant Design size values remain accepted while consumers migrate. */
export type UICompatibleSize = UISize | 'small' | 'middle' | 'default' | 'large';

export type UIAntSize = 'small' | 'middle' | 'large';

export function toAntSize(size: UICompatibleSize | undefined, fallback: UIAntSize = 'middle'): UIAntSize {
  if (size === undefined) return fallback;

  switch (size) {
    case 'sm':
    case 'small': return 'small';
    case 'lg':
    case 'large': return 'large';
    case 'md':
    case 'middle':
    case 'default': return 'middle';
  }
}
