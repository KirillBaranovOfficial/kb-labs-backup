/**
 * UISkeleton component - Loading skeleton
 *
 * Wraps Ant Design Skeleton with simplified API.
 * NO hardcoded colors, uses Ant Design theme.
  * @demoArgs {"loading":true,"avatar":true,"avatarShape":"circle","title":true,"active":true,"lines":3}
 */

import * as React from 'react';
import { Skeleton as AntSkeleton } from 'antd';
import clsx from 'clsx';
import styles from './UISkeleton.module.css';

export interface UISkeletonProps {
  /** Show loading state */
  loading?: boolean;
  /** Content to show when not loading */
  children?: React.ReactNode;
  /** Show avatar */
  avatar?: boolean;
  /** Avatar shape */
  avatarShape?: 'circle' | 'square';
  /** Number of paragraph lines */
  lines?: number;
  /** Show title */
  title?: boolean;
  /** Active animation */
  active?: boolean;
  /** Additional CSS class */
  className?: string;
  /** Additional styles */
  style?: React.CSSProperties;
}

/**
 * UISkeleton - Loading skeleton
 *
 * @example
 * ```tsx
 * <UISkeleton loading={isLoading}>
 *   <div>Content</div>
 * </UISkeleton>
 *
 * <UISkeleton avatar lines={3} active />
 *
 * <UISkeleton
 *   loading={isLoading}
 *   avatar
 *   avatarShape="circle"
 *   title
 *   lines={2}
 * >
 *   <ProfileCard />
 * </UISkeleton>
 * ```
 */
export function UISkeleton({
  loading = true,
  children,
  avatar,
  avatarShape = 'circle',
  lines = 3,
  title = true,
  active = true,
  className,
  style,
}: UISkeletonProps) {
  return (
    <AntSkeleton
      loading={loading}
      avatar={avatar ? { shape: avatarShape } : false}
      paragraph={{ rows: lines }}
      title={title}
      active={active}
      className={clsx(styles.skeleton, className)}
      style={style}
    >
      {children}
    </AntSkeleton>
  );
}

/**
 * UISkeletonButton - Button skeleton
 */
export function UISkeletonButton(props: React.ComponentProps<typeof AntSkeleton.Button>) {
  return <AntSkeleton.Button {...props} />;
}

/**
 * UISkeletonInput - Input skeleton
 */
export function UISkeletonInput(props: React.ComponentProps<typeof AntSkeleton.Input>) {
  return <AntSkeleton.Input {...props} />;
}

/**
 * UISkeletonImage - Image skeleton
 */
export function UISkeletonImage(props: React.ComponentProps<typeof AntSkeleton.Image>) {
  return <AntSkeleton.Image {...props} />;
}

/**
 * UISkeletonNode - Custom node skeleton
 */
export function UISkeletonNode(props: React.ComponentProps<typeof AntSkeleton.Node>) {
  return <AntSkeleton.Node {...props} />;
}

export interface UISkeletonTextProps {
  /**
   * Width of the text line(s). Can be a number (px), string ('100%', '8ch'), or
   * an array to render multiple lines with different widths.
   * @default '100%'
   */
  width?: number | string | Array<number | string>;
  /**
   * Height / font-size of the shimmer block — matches the surrounding text size.
   * @default '1em'
   */
  height?: number | string;
  /** Gap between lines when width is an array */
  gap?: number | string;
  /** Additional CSS class */
  className?: string;
  /** Additional styles applied to the wrapper */
  style?: React.CSSProperties;
}

/**
 * UISkeletonText - Inline text shimmer placeholder
 *
 * Renders a shimmer block that matches the height of surrounding text.
 * Uses Ant Design's native skeleton animation so it respects the theme.
 *
 * @example
 * // Single line, full width
 * <UISkeletonText />
 *
 * // Single line, fixed width
 * <UISkeletonText width={120} height="1em" />
 *
 * // Multiple lines with varying widths (like a paragraph)
 * <UISkeletonText width={['100%', '85%', '60%']} height="14px" gap={8} />
 *
 * // Match a specific font size
 * <UISkeletonText width="8ch" height="20px" />
 */
export function UISkeletonText({
  width = '100%',
  height = '1em',
  gap = 6,
  className,
  style,
}: UISkeletonTextProps) {
  const isArray = Array.isArray(width);
  const widths = isArray ? width : [width];
  const outerWidth = isArray ? '100%' : (typeof width === 'number' ? `${width}px` : width);

  return (
    <span
      className={className}
      style={{ display: 'inline-flex', flexDirection: 'column', gap, width: outerWidth, ...style }}
    >
      {widths.map((w, i) => (
        <span
          key={i}
          className={clsx(styles.skeletonText)}
          style={{
            display: 'block',
            width: isArray ? (typeof w === 'number' ? `${w}px` : w) : '100%',
            height: typeof height === 'number' ? `${height}px` : height,
            borderRadius: 3,
            flexShrink: 0,
          }}
        />
      ))}
    </span>
  );
}

export interface UISkeletonTableProps {
  /** Grid column widths. Numbers are relative fractions, strings are CSS widths. */
  columns?: Array<number | string>;
  /** Number of placeholder rows. */
  rows?: number;
  /** Render the muted table header. */
  showHeader?: boolean;
  /** Accessible loading label. */
  label?: string;
  className?: string;
  style?: React.CSSProperties;
}

export interface UISkeletonChartProps {
  /** Placeholder height. */
  height?: number | string;
  /** Number of muted series markers above the plot. */
  series?: number;
  /** Number of silhouettes in the plot. */
  bars?: number;
  /** Accessible loading label. */
  label?: string;
  className?: string;
  style?: React.CSSProperties;
}

const DEFAULT_TABLE_COLUMNS: Array<number | string> = ['100px', '2fr', '100px', '120px', '110px'];

/** A low contrast table silhouette that preserves the space used by incoming rows. */
export function UISkeletonTable({
  columns = DEFAULT_TABLE_COLUMNS,
  rows = 5,
  showHeader = true,
  label = 'Loading table',
  className,
  style,
}: UISkeletonTableProps) {
  const gridTemplateColumns = columns
    .map((column) => typeof column === 'number' ? `${column}fr` : column)
    .join(' ');
  const gridStyle = { '--table-columns': gridTemplateColumns, ...style } as React.CSSProperties;

  return (
    <div className={clsx(styles.tableSkeleton, className)} style={gridStyle} role="status" aria-label={label}>
      {showHeader && (
        <div className={styles.tableHeader} aria-hidden="true">
          {columns.map((_, columnIndex) => (
            <span
              key={`header-${columnIndex}`}
              className={styles.bar}
              style={{ '--bar-width': `${48 + (columnIndex % 3) * 10}%` } as React.CSSProperties}
            />
          ))}
        </div>
      )}
      {Array.from({ length: Math.max(0, rows) }, (_, rowIndex) => (
        <div className={styles.tableRow} key={`row-${rowIndex}`} aria-hidden="true">
          {columns.map((_, columnIndex) => {
            const width = columnIndex === 0
              ? 62 + (rowIndex % 3) * 8
              : 48 + ((rowIndex + columnIndex) % 4) * 12;
            return (
              <span
                key={`cell-${columnIndex}`}
                className={styles.bar}
                style={{ '--bar-width': `${width}%` } as React.CSSProperties}
              />
            );
          })}
        </div>
      ))}
      <span className={styles.srOnly}>{label}</span>
    </div>
  );
}

/** Chart-shaped placeholder for data visualizations. */
export function UISkeletonChart({
  height = 240,
  series = 2,
  bars = 12,
  label = 'Loading chart',
  className,
  style,
}: UISkeletonChartProps) {
  const chartStyle = { '--chart-height': typeof height === 'number' ? `${height}px` : height, ...style } as React.CSSProperties;

  return (
    <div className={clsx(styles.chartSkeleton, className)} style={chartStyle} role="status" aria-label={label}>
      <div className={styles.chartLegend} aria-hidden="true">
        {Array.from({ length: Math.max(1, series) }, (_, index) => (
          <span className={styles.legendItem} key={`series-${index}`}>
            <span className={styles.legendMark} />
            <span className={styles.legendLabel} />
          </span>
        ))}
      </div>
      <div className={styles.chartPlot} aria-hidden="true">
        {[0, 1, 2, 3].map((line) => <span className={styles.chartGridLine} key={`grid-${line}`} />)}
        <div className={styles.chartBars}>
          {Array.from({ length: Math.max(3, bars) }, (_, index) => (
            <span
              className={styles.chartBar}
              key={`bar-${index}`}
              style={{ '--bar-height': `${24 + ((index * 37) % 64)}%` } as React.CSSProperties}
            />
          ))}
        </div>
      </div>
      <div className={styles.chartAxis} aria-hidden="true">
        {[0, 1, 2, 3, 4, 5].map((labelIndex) => <span className={styles.axisLabel} key={`axis-${labelIndex}`} />)}
      </div>
      <span className={styles.srOnly}>{label}</span>
    </div>
  );
}
