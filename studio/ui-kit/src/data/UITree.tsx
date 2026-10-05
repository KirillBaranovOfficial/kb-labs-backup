/**
 * UITree component - Tree view
 *
 * Wraps Ant Design Tree for hierarchical data display.
 */

import * as React from 'react';
import { Tree as AntTree, type TreeProps as AntTreeProps } from 'antd';
import clsx from 'clsx';
import styles from './UITree.module.css';

export type UITreeSize = 'sm' | 'md' | 'lg';

export interface UITreeProps extends Omit<AntTreeProps, 'size'> {
  /** Density and row height for the whole tree. */
  size?: UITreeSize;
}

export function UITree({ size = 'md', className, showLine = true, blockNode = true, ...props }: UITreeProps) {
  return (
    <AntTree
      {...props}
      className={clsx(styles.tree, styles[size], className)}
      showLine={showLine}
      blockNode={blockNode}
    />
  );
}

export function UITreeDirectoryTree(props: React.ComponentProps<typeof AntTree.DirectoryTree>) {
  return <AntTree.DirectoryTree {...props} />;
}

export function UITreeNode(props: React.ComponentProps<typeof AntTree.TreeNode>) {
  return <AntTree.TreeNode {...props} />;
}
