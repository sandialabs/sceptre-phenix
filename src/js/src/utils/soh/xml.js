// XML text helpers for the SVG and GEXF exports.

// Characters XML 1.0 does not allow at all (C0 controls other than tab, LF
// and CR, lone surrogates, U+FFFE and U+FFFF); dropped rather than escaped.
const INVALID_XML =
  // eslint-disable-next-line no-control-regex
  /[\u0000-\u0008\u000B\u000C\u000E-\u001F￾￿]|[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/g;

// Escapes text for use in XML content or a double- or single-quoted
// attribute. Tabs and line breaks become character references so that
// attribute values keep them instead of being normalized to spaces.
export function escapeXml(value) {
  return String(value ?? '')
    .replace(INVALID_XML, '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;')
    .replace(/\t/g, '&#9;')
    .replace(/\n/g, '&#10;')
    .replace(/\r/g, '&#13;');
}

// Renders attributes, skipping undefined, null, NaN and empty values.
export function xmlAttrs(attrs) {
  return Object.entries(attrs)
    .filter(
      ([, v]) => v !== undefined && v !== null && !Number.isNaN(v) && v !== '',
    )
    .map(([k, v]) => ` ${k}="${escapeXml(v)}"`)
    .join('');
}
