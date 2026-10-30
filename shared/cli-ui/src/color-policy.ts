const isTruthyEnv = (value: string | undefined): boolean => {
  if (!value) {return false;}
  const normalized = value.trim().toLowerCase();
  return normalized !== '' && normalized !== '0' && normalized !== 'false' && normalized !== 'off';
};

export function resolveColorSupport(isTTY: boolean | undefined, noColor: string | undefined, forceColor: string | undefined): boolean {
  if (isTruthyEnv(noColor)) return false;
  if (isTruthyEnv(forceColor)) return true;
  return isTTY === true;
}
