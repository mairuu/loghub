export interface Upload {
  /** The request body: one JSON value per line. */
  body: string
  /** How many records the body holds, blank lines aside. */
  records: number
}

/**
 * Turns an uploaded file into the NDJSON the ingest endpoint takes. A file
 * that is a single JSON document, an object or an array of them, is split
 * into one line per record; the samples are pretty-printed objects. Anything
 * else is taken to be NDJSON already and sent as it is, for the server to
 * judge line by line.
 */
export function toNdjson(text: string): Upload {
  let doc: unknown
  try {
    doc = JSON.parse(text)
  } catch {
    const body = text.replace(/\r\n/g, '\n')
    return { body, records: body.split('\n').filter((line) => line.trim() !== '').length }
  }
  const records = Array.isArray(doc) ? doc : [doc]
  return { body: records.map((r) => JSON.stringify(r)).join('\n'), records: records.length }
}
