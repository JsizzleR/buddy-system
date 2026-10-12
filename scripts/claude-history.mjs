// Qualification helper, not an adapter backend. Import only the explicitly
// supplied, version-checked SDK; never install a package or launch query().
// Captures contain disposable fixture conversations, not operator history.
import { readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const [sdk, session, worktree, missing] = process.argv.slice(2);
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
if (process.argv.length !== 6 || !path.isAbsolute(sdk ?? '') ||
    !path.isAbsolute(worktree ?? '') || !path.isAbsolute(process.env.CLAUDE_CONFIG_DIR ?? '') ||
    !uuid.test(session ?? '') || !uuid.test(missing ?? '') || session === missing) {
  throw new Error('require explicit SDK, native state, worktree and distinct session UUIDs');
}
const pkg = JSON.parse(await readFile(path.join(sdk, 'package.json'), 'utf8'));
if (pkg.name !== '@anthropic-ai/claude-agent-sdk' || pkg.version !== '0.3.202') {
  throw new Error('require inspected @anthropic-ai/claude-agent-sdk 0.3.202');
}
const sdkHashes = Object.fromEntries(await Promise.all(
  ['package.json', 'sdk.mjs', 'sdk.d.ts'].map(async name =>
    [name, createHash('sha256').update(await readFile(path.join(sdk, name))).digest('hex')])));
const { getSessionMessages } = await import(pathToFileURL(path.join(sdk, 'sdk.mjs')).href);
const messages = await getSessionMessages(session, { dir: worktree });
const systemMessages = await getSessionMessages(session, { dir: worktree, includeSystemMessages: true });
const missingMessages = await getSessionMessages(missing, { dir: worktree, includeSystemMessages: true });
process.stdout.write(JSON.stringify({ schema: 'buddy.claude-history-lookup.v1', sdk_version: pkg.version,
  sdk_sha256: sdkHashes,
  session_id: session, worktree, missing_id: missing, messages, system_messages: systemMessages,
  missing_messages: missingMessages }) + '\n');
