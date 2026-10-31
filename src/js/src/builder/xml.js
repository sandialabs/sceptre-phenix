// A small XML writer for the GEXF and draw.io files that Download makes.
// Element names come only from code. Every attribute value and text is escaped.

/** Characters XML 1.0 does not allow, lone surrogates among them. */
export const NOT_XML =
  /[^\t\n\r\u0020-\uD7FF\uE000-\uFFFD\u{10000}-\u{10FFFF}]/gu;

function xmlText(value) {
  return String(value)
    .replace(NOT_XML, '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/\r/g, '&#13;');
}

// Tabs and line breaks are written as references: a parser turns them into
// spaces otherwise.
function xmlAttribute(value) {
  return xmlText(value)
    .replace(/"/g, '&quot;')
    .replace(/\t/g, '&#9;')
    .replace(/\n/g, '&#10;');
}

/** An element for writeElement. An undefined attribute value is not written. */
export function element(name, attributes = {}, children = []) {
  return { name, attributes, children };
}

/** Writes an element and its children as XML lines, two spaces of indent for each level. */
export function writeElement(item, depth, lines) {
  const indent = '  '.repeat(depth);
  const attributes = Object.entries(item.attributes)
    .filter(([, value]) => value !== undefined)
    .map(([name, value]) => ` ${name}="${xmlAttribute(value)}"`)
    .join('');
  const { name, children } = item;

  if (children.length === 0) {
    lines.push(`${indent}<${name}${attributes}/>`);
  } else if (children.every((child) => typeof child === 'string')) {
    lines.push(
      `${indent}<${name}${attributes}>${xmlText(children.join(''))}</${name}>`,
    );
  } else {
    lines.push(`${indent}<${name}${attributes}>`);
    for (const child of children) {
      writeElement(child, depth + 1, lines);
    }
    lines.push(`${indent}</${name}>`);
  }
}
