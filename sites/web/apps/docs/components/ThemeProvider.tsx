'use client';

import { createContext, useContext, useEffect, useLayoutEffect, useState } from 'react';

type ThemeMode = 'light' | 'dark' | 'system';
type ResolvedTheme = 'light' | 'dark';

const ThemeContext = createContext<{ theme: ResolvedTheme; mode: ThemeMode; setMode: (mode: ThemeMode) => void; toggle: () => void }>({
  theme: 'light',
  mode: 'system',
  setMode: () => {},
  toggle: () => {},
});

export function useTheme() {
  return useContext(ThemeContext);
}

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  // Keep the first client render identical to the server render. The theme
  // bootstrap script may already have updated <html>, so adopt that value only
  // after hydration has committed.
  const [theme, setTheme] = useState<ResolvedTheme>('light');
  const [mode, setModeState] = useState<ThemeMode>('system');

  useLayoutEffect(() => {
    const bootstrappedMode = document.documentElement.getAttribute('data-theme-mode');
    const bootstrappedTheme = document.documentElement.getAttribute('data-theme');
    if (bootstrappedMode === 'dark' || bootstrappedMode === 'light' || bootstrappedMode === 'system') setModeState(bootstrappedMode);
    if (bootstrappedTheme === 'dark' || bootstrappedTheme === 'light') setTheme(bootstrappedTheme);
  }, []);

  useLayoutEffect(() => {
    document.documentElement.setAttribute('data-theme', theme);
    document.documentElement.setAttribute('data-theme-mode', mode);
  }, [mode, theme]);

  useEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)');
    const updateFromSystem = () => {
      if (mode === 'system') setTheme(media.matches ? 'dark' : 'light');
    };
    media.addEventListener('change', updateFromSystem);
    updateFromSystem();
    return () => media.removeEventListener('change', updateFromSystem);
  }, [mode]);

  const toggle = () => {
    const next: ThemeMode = mode === 'system' ? 'light' : mode === 'light' ? 'dark' : 'system';
    setMode(next);
  };

  const setMode = (next: ThemeMode) => {
    localStorage.setItem('docs-theme', next);
    setModeState(next);
    if (next === 'system') setTheme(window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
    else setTheme(next);
  };

  return (
    <ThemeContext.Provider value={{ theme, mode, setMode, toggle }}>
      {children}
    </ThemeContext.Provider>
  );
}
