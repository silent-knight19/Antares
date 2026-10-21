// Split an inline command into arguments. Double quotes group whitespace and
// accept a backslash escape; single quotes are literal. Redis's full
// sdssplitargs grammar is not replicated in M1.

export function parseCommand(raw: string): string[] {
  const args: string[] = [];
  let current = '';
  let started = false;
  let quoted = false;
  let escaped = false;

  for (const char of raw) {
    if (escaped) {
      current += char;
      escaped = false;
      started = true;
      continue;
    }
    if (quoted && char === '\\') {
      escaped = true;
      started = true;
      continue;
    }
    if (char === '"') {
      quoted = !quoted;
      started = true;
      continue;
    }
    if (!quoted && /\s/.test(char)) {
      if (started) {
        args.push(current);
        current = '';
        started = false;
      }
      continue;
    }
    current += char;
    started = true;
  }

  if (quoted) {
    throw new Error('unbalanced quote');
  }
  if (started) {
    args.push(current);
  }
  return args;
}
