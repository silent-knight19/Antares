// Split an inline command into arguments using Redis's sdssplitargs rules:
// whitespace separates arguments, double quotes accept backslash escapes (and
// \xHH), and single quotes accept only \'. A closing quote must be followed by
// whitespace or the end of the line.

const escapes: Record<string, string> = {
  n: '\n',
  r: '\r',
  t: '\t',
  b: '\b',
  a: '\x07',
};

function isHex(char: string | undefined): boolean {
  return char !== undefined && /[0-9a-fA-F]/.test(char);
}

export function parseCommand(raw: string): string[] {
  const args: string[] = [];
  let i = 0;

  while (i < raw.length) {
    while (i < raw.length && /\s/.test(raw[i])) i++;
    if (i >= raw.length) break;

    let arg = '';
    let quote: '"' | "'" | null = null;
    while (i < raw.length) {
      const char = raw[i];
      if (quote === '"') {
        if (char === '\\' && i + 1 < raw.length) {
          const next = raw[i + 1];
          if (next === 'x' && isHex(raw[i + 2]) && isHex(raw[i + 3])) {
            arg += String.fromCharCode(parseInt(raw.slice(i + 2, i + 4), 16));
            i += 4;
            continue;
          }
          arg += escapes[next] ?? next;
          i += 2;
          continue;
        }
        if (char === '"') {
          if (i + 1 < raw.length && !/\s/.test(raw[i + 1])) {
            throw new Error('unbalanced quote');
          }
          quote = null;
          i++;
          break;
        }
        arg += char;
        i++;
      } else if (quote === "'") {
        if (char === '\\' && raw[i + 1] === "'") {
          arg += "'";
          i += 2;
          continue;
        }
        if (char === "'") {
          if (i + 1 < raw.length && !/\s/.test(raw[i + 1])) {
            throw new Error('unbalanced quote');
          }
          quote = null;
          i++;
          break;
        }
        arg += char;
        i++;
      } else {
        if (/\s/.test(char)) break;
        if (char === '"' || char === "'") {
          quote = char;
          i++;
          continue;
        }
        arg += char;
        i++;
      }
    }
    if (quote) throw new Error('unbalanced quote');
    args.push(arg);
  }

  return args;
}
