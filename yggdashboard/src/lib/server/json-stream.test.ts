import { describe, it, expect } from 'vitest';
import { extractJSONValues } from './json-stream';

describe('extractJSONValues', () => {
  it('returns nothing for an empty buffer', () => {
    expect(extractJSONValues('')).toEqual({ values: [], rest: '' });
  });

  it('extracts a single complete JSON object', () => {
    const result = extractJSONValues('{"a":1}');
    expect(result).toEqual({ values: [{ a: 1 }], rest: '' });
  });

  it('leaves a partial JSON object in rest, extracting nothing', () => {
    const result = extractJSONValues('{"a":1,"b":');
    expect(result.values).toEqual([]);
    expect(result.rest).toBe('{"a":1,"b":');
  });

  it('extracts multiple values concatenated with no separator (simulates two Encode() calls arriving in one TCP read)', () => {
    const result = extractJSONValues('{"a":1}{"b":2}');
    expect(result).toEqual({ values: [{ a: 1 }, { b: 2 }], rest: '' });
  });

  it("extracts multiple values separated by newlines (matches encoding/json.Encoder's trailing newline)", () => {
    const result = extractJSONValues('{"a":1}\n{"b":2}\n');
    expect(result).toEqual({ values: [{ a: 1 }, { b: 2 }], rest: '' });
  });

  it('extracts complete values and leaves a trailing partial one in rest', () => {
    const result = extractJSONValues('{"a":1}{"b":2');
    expect(result.values).toEqual([{ a: 1 }]);
    expect(result.rest).toBe('{"b":2');
  });

  it('does not miscount braces that appear inside a JSON string', () => {
    const result = extractJSONValues('{"a":"} { not real braces"}');
    expect(result).toEqual({ values: [{ a: '} { not real braces' }], rest: '' });
  });

  it('handles an escaped quote inside a string without ending the string early', () => {
    const result = extractJSONValues('{"a":"quote: \\" still inside"}{"b":2}');
    expect(result).toEqual({
      values: [{ a: 'quote: " still inside' }, { b: 2 }],
      rest: ''
    });
  });

  it('handles nested objects and arrays', () => {
    const result = extractJSONValues('{"a":{"nested":[1,2,{"deep":true}]}}');
    expect(result).toEqual({
      values: [{ a: { nested: [1, 2, { deep: true }] } }],
      rest: ''
    });
  });
});
