# kb-runner

`kb-runner` coordinates agent jobs and tracks their provider usage. Codex is the default vendor. z.ai is an explicit reserve: it runs only when `agent start --vendor zai` is supplied, and it is never selected automatically or used as a fallback.

## Run an explicit z.ai job

The z.ai vendor uses the installed Claude Code CLI and the Anthropic-compatible GLM Coding Plan endpoint. Create an owner-only key file and point the runner at its path. Paste the key into the prompt; it is not accepted from an environment variable.

```sh
mkdir -p "$HOME/.config/kb-runner"
chmod 700 "$HOME/.config/kb-runner"
(umask 077; cat > "$HOME/.config/kb-runner/zai.key")
chmod 600 "$HOME/.config/kb-runner/zai.key"
export KB_RUNNER_ZAI_KEY_FILE="$HOME/.config/kb-runner/zai.key"
RUNNER_STATE="$HOME/.local/state/kb-runner"

kb-runner agent start \
  --vendor zai \
  --state "$RUNNER_STATE" \
  --stream reserve-task \
  --workdir "$PWD" \
  --prompt-file /path/to/prompt.md
```

The runner rejects a missing, empty, unreadable, non-regular, or group/other-accessible key file. z.ai jobs get a separate Claude config directory under the runner state directory. The default model mapping is Opus → `glm-5.3`, Sonnet → `glm-5.3`, and Haiku → `glm-5.3-flash`. Override an individual mapping with `ZAI_OPUS_MODEL`, `ZAI_SONNET_MODEL`, or `ZAI_HAIKU_MODEL`.

The endpoint and model IDs follow the [official z.ai documentation](https://zcode.z.ai/en/docs/configuration). The Anthropic endpoint applies to Coding Plan usage; it is not the general prepaid-balance endpoint.

## Record and inspect the spend grant

Record the owner’s grant once. The ledger adds input and output token usage from each z.ai job, keeps per-job totals, and shows the most recent job, total spend, and remaining grant in `limits`.

```sh
kb-runner limits record zai --state "$RUNNER_STATE" --grant 500M
kb-runner limits --state "$RUNNER_STATE"
```

The grant is a recorded ceiling for display. It does not participate in provider selection or automatic fallback. Claude and Codex recorded/vendor window pacing is unchanged.

## Run an explicit ZCode account job

ZCode uses its own account login and token store. Log in once with the same wrapper that `kb-runner` uses; the runner does not read or copy the credentials:

```sh
"$HOME/Desktop/work/zcode/zcode" login zai
RUNNER_STATE="$HOME/.local/state/kb-runner"

kb-runner agent start \
  --vendor zcode \
  --state "$RUNNER_STATE" \
  --stream zcode-reserve-task \
  --workdir "$PWD" \
  --prompt-file /path/to/prompt.md
```

`zcode` is available only when named with `--vendor zcode`; automatic selection and fallback never use it. Set `ZCODE_BIN` if the wrapper is elsewhere. The child environment points `ZCODE_DATA_BASE_DIR` to `$HOME/.zcode-kb-runner`, matching the wrapper's login data directory, and filters inherited Claude, Codex, Anthropic, and ZCode settings.

The Start Plan account selection is read from `$HOME/.zcode-kb-runner/.zcode/v2/provider_config.json`. For the owner's current setup it selects provider `account:zai-start-plan`, model `GLM-5.3`, and `options.reasoningLevel: "max"`. `kb-runner` does not pass a model flag or set a provider/model override, so the configured account selection is used.

ZCode runs in `yolo` mode for unattended work. The runner wraps it in macOS `sandbox-exec`; writes are allowed in the agent worktree, per-job temp directory, ZCode data directory, and Node/pnpm caches. Network access remains enabled for model requests. If the OS sandbox is unavailable or cannot apply its profile, the job is refused; it never runs unsandboxed.

The final ZCode `stream-json` result and `--json` summary include `usage.inputTokens` and `usage.outputTokens`; those values contribute to the per-job and vendor ledger. Record the grant shown in the account and inspect remaining tokens with:

```sh
kb-runner limits record zcode --state "$RUNNER_STATE" --grant 500M
kb-runner limits --state "$RUNNER_STATE"
```

The grant is accounting only and does not select or fall back to ZCode.
