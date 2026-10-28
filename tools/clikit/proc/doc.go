//go:build darwin || linux

// Package proc owns supervised command runs, process membership, teardown,
// and lifecycle events. It supports Linux and macOS; Windows is unsupported.
//
// Ownership guarantee: the keeper reaps same-UID descendants while they remain
// attributable through an inherited run-token descriptor, a readable run
// marker, observed ancestry, or a previously recorded PID/start identity. On
// Linux the keeper also adopts orphaned descendants as a child subreaper. The
// token identity is matched across exec, setsid, and reparenting. A previously
// observed process that later loses its token and has no readable marker remains
// visible as unverified until teardown resolves it.
//
// Two cases are outside this guarantee. The owner does not inspect foreign-UID
// descriptor tables, and supervised runs do not have privilege to change UID,
// so cross-UID descendants are not attributed. macOS has no subreaper or
// cgroup; a same-UID descendant that closes every inherited descriptor, has no
// readable matching marker, and detaches before the first observation cannot be
// attributed after losing ancestry. In particular, a descendant that closes
// inherited fd 3 and clears KBLABS_CLIPROC_RUN before the first process snapshot
// is reported as no-run-marker-or-parent-history and is omitted from owned
// cleanup inventory; this close-before-first-observation interleaving is outside
// the guarantee. Cleanup is bounded and reports unresolved identities rather
// than claiming that unobservable processes were reaped.
package proc
