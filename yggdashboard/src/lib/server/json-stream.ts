/**
 * Extracts every complete top-level JSON value from the front of buffer,
 * in order. The admin socket protocol (src/admin/admin.go) has no length
 * prefix or delimiter - encoding/json's Decoder/Encoder just write and
 * read back-to-back JSON values on the raw stream - so a client has to
 * track object/array depth itself (respecting strings and escapes) to
 * find where one value ends and the next begins. Any trailing bytes that
 * don't yet form a complete value are returned as `rest`, to be
 * prepended to the next chunk read from the socket.
 */
export function extractJSONValues(buffer: string): { values: unknown[]; rest: string } {
  const values: unknown[] = [];
  let i = 0;

  while (i < buffer.length) {
    while (i < buffer.length && /\s/.test(buffer[i])) i++;
    if (i >= buffer.length) break;

    const start = i;
    let depth = 0;
    let inString = false;
    let escaped = false;
    let end = -1;

    for (; i < buffer.length; i++) {
      const ch = buffer[i];
      if (inString) {
        if (escaped) {
          escaped = false;
        } else if (ch === '\\') {
          escaped = true;
        } else if (ch === '"') {
          inString = false;
        }
        continue;
      }
      if (ch === '"') {
        inString = true;
      } else if (ch === '{' || ch === '[') {
        depth++;
      } else if (ch === '}' || ch === ']') {
        depth--;
        if (depth === 0) {
          end = i + 1;
          i++;
          break;
        }
      }
    }

    if (end === -1) {
      return { values, rest: buffer.slice(start) };
    }
    values.push(JSON.parse(buffer.slice(start, end)));
  }

  return { values, rest: '' };
}
