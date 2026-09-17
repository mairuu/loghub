import { describe, expect, it } from 'vitest'
import { toNdjson } from './ndjson'

describe('toNdjson', () => {
  it('puts a pretty-printed object on one line', () => {
    const text = '{\n  "tenant": "demoA",\n  "source": "ad",\n  "raw": { "a": [1, 2] }\n}\n'
    expect(toNdjson(text)).toEqual({ body: '{"tenant":"demoA","source":"ad","raw":{"a":[1,2]}}', records: 1 })
  })

  it('splits an array into records', () => {
    expect(toNdjson('[{"a":1},\n {"b":2}]')).toEqual({ body: '{"a":1}\n{"b":2}', records: 2 })
  })

  it('sends NDJSON as it is, apart from line endings', () => {
    const text = '{"a":1}\r\n\r\n{"b":2}\r\nnot json\r\n'
    expect(toNdjson(text)).toEqual({ body: '{"a":1}\n\n{"b":2}\nnot json\n', records: 3 })
  })

  it('sends a single JSON value that is not an object for the server to refuse', () => {
    expect(toNdjson('42')).toEqual({ body: '42', records: 1 })
  })
})
