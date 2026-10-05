'use client';

import { useEffect, useState } from 'react';
import { codeToHtml } from 'shiki';
import { docsShikiThemes } from '@/lib/shiki-themes';

const highlightCache = new Map<string, string>();

/** Shared client-side Shiki renderer for code that changes interactively. */
export function HighlightedCode({ code, language = 'tsx' }: { code: string; language?: string }) {
  const [html, setHtml] = useState('');

  useEffect(() => {
    let current = true;
    const cacheKey = `${language}\u0000${code}`;
    const cached = highlightCache.get(cacheKey);
    if (cached) {
      setHtml(cached);
      return () => { current = false; };
    }

    void codeToHtml(code, { lang: language, themes: docsShikiThemes, defaultColor: false })
      .then((highlighted) => {
        highlightCache.set(cacheKey, highlighted);
        if (current) setHtml(highlighted);
      })
      .catch(() => {
        if (current) setHtml('');
      });

    return () => { current = false; };
  }, [code, language]);

  if (!html) return <pre className="uiKitPlayground__codeFallback"><code>{code}</code></pre>;
  return <div className="uiKitPlayground__highlightedCode" dangerouslySetInnerHTML={{ __html: html }} />;
}
