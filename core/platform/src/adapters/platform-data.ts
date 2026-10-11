/** Platform-owned adapter files resolve against one root from the layout contract. */
export interface PlatformDataContext {
  /** Development `.kb/data`, or the installed `state/data` projection. */
  readonly dataRoot: string;
}
