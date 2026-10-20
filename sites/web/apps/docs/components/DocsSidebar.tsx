'use client';

import { useState, useEffect } from 'react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import type { NavGroup } from '@/nav.config';
import s from './DocsSidebar.module.css';

type DocsSidebarProps = {
  nav: NavGroup[];
};

function ChevronIcon({ open }: { open: boolean }) {
  return (
    <svg
      className={`${s.chevron}${open ? ` ${s.chevronOpen}` : ''}`}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M5.5 6.5L8 9l2.5-2.5" />
    </svg>
  );
}

function itemContainsPath(item: NavGroup['items'][number], pathname: string): boolean {
  return pathname === item.href
    || pathname.startsWith(item.href + '/')
    || item.children?.some((child) => itemContainsPath(child, pathname)) === true;
}

function NavTreeItem({ item, pathname }: { item: NavGroup['items'][number]; pathname: string }) {
  const active = pathname === item.href;
  const hasChildren = (item.children?.length ?? 0) > 0;
  const containsActive = itemContainsPath(item, pathname);
  const [open, setOpen] = useState(containsActive);

  useEffect(() => {
    if (containsActive) setOpen(true);
  }, [containsActive]);

  return (
    <li>
      <div className={hasChildren ? s.itemRow : undefined}>
        <Link
          href={item.href}
          className={`${s.item}${active ? ` ${s.itemActive}` : ''}`}
          aria-current={active ? 'page' : undefined}
        >
          {item.label}
          {item.untranslated && <span className={s.untranslatedTag}>EN</span>}
        </Link>
        {hasChildren && (
          <button
            className={s.itemToggle}
            onClick={() => setOpen((value) => !value)}
            aria-label={`${open ? 'Collapse' : 'Expand'} ${item.label}`}
            aria-expanded={open}
          >
            <ChevronIcon open={open} />
          </button>
        )}
      </div>
      {hasChildren && open && (
        <ul className={s.subItems}>
          {item.children?.map((child) => (
            <NavTreeItem key={child.href} item={child} pathname={pathname} />
          ))}
        </ul>
      )}
    </li>
  );
}

function NavGroupSection({ group, pathname }: { group: NavGroup; pathname: string }) {
  const isActiveGroup = group.items.some((item) => itemContainsPath(item, pathname));
  const [open, setOpen] = useState(isActiveGroup);

  // Keep open state in sync when navigating
  useEffect(() => {
    if (isActiveGroup) setOpen(true);
  }, [isActiveGroup]);

  return (
    <div className={s.group}>
      <button
        className={s.groupTitle}
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
      >
        {group.title}
        <ChevronIcon open={open} />
      </button>

      {open && (
        <ul className={s.groupItems}>
          {group.items.map((item) => <NavTreeItem key={item.href} item={item} pathname={pathname} />)}
        </ul>
      )}
    </div>
  );
}

export function DocsSidebar({ nav }: DocsSidebarProps) {
  const pathname = usePathname();

  // Separate "Start Here" (top-level flat items) from collapsible groups
  const [startHere, ...groups] = nav;

  return (
    <nav className={s.sidebar} aria-label="Documentation navigation">
      {/* Top-level items (Quick Start, Installation) — always visible */}
      {startHere && (
        <div className={s.topItems}>
          {startHere.items.map((item) => {
            const active = pathname === item.href;
            return (
              <Link
                key={item.href}
                href={item.href}
                className={`${s.item}${active ? ` ${s.itemActive}` : ''}`}
              >
                {item.label}
                {item.untranslated && <span className={s.untranslatedTag}>EN</span>}
              </Link>
            );
          })}
        </div>
      )}

      <div className={s.divider} />

      {/* Collapsible groups */}
      {groups.map((group) => (
        <NavGroupSection key={group.title} group={group} pathname={pathname} />
      ))}
    </nav>
  );
}
