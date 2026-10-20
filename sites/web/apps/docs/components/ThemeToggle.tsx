'use client';

import { useTheme } from './ThemeProvider';
import s from './ThemeToggle.module.css';
import { useTranslations } from 'next-intl';

function SunIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" aria-hidden="true">
      <circle cx="8" cy="8" r="3" />
      <line x1="8" y1="1" x2="8" y2="2.5" />
      <line x1="8" y1="13.5" x2="8" y2="15" />
      <line x1="1" y1="8" x2="2.5" y2="8" />
      <line x1="13.5" y1="8" x2="15" y2="8" />
      <line x1="3.05" y1="3.05" x2="4.1" y2="4.1" />
      <line x1="11.9" y1="11.9" x2="12.95" y2="12.95" />
      <line x1="12.95" y1="3.05" x2="11.9" y2="4.1" />
      <line x1="4.1" y1="11.9" x2="3.05" y2="12.95" />
    </svg>
  );
}

function MoonIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" aria-hidden="true">
      <path d="M13.5 10A6 6 0 0 1 6 2.5a6 6 0 1 0 7.5 7.5z" />
    </svg>
  );
}

function SystemIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="1.5" y="2" width="13" height="9" rx="1.5" />
      <path d="M5.5 14h5M8 11v3" />
    </svg>
  );
}

export function ThemeToggle() {
  const t = useTranslations('header');
  const { mode, toggle } = useTheme();
  const modeLabel = t(`theme.${mode}`);
  const nextMode = mode === 'system' ? 'light' : mode === 'light' ? 'dark' : 'system';
  const label = t('theme.current', { mode: modeLabel, next: t('theme.switchTo', { mode: t(`theme.${nextMode}`) }) });
  return (
    <button
      className={s.btn}
      onClick={toggle}
      aria-label={label}
      title={label}
      type="button"
    >
      {mode === 'system' ? <SystemIcon /> : mode === 'light' ? <MoonIcon /> : <SunIcon />}
    </button>
  );
}
