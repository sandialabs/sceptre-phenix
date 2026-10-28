// A strict reader of the XML that Download writes (GEXF and draw.io):
// anything but the declaration, elements, quoted attributes and text, a
// misnested tag or a bare & throws.
const XML_TOKEN =
  /<\?xml[^?]*\?>|<(\/?)([\w:.-]+)((?:\s+[\w:.-]+="[^"<]*")*)\s*(\/?)>|([^<]+)/y;
const XML_REFERENCES = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'" };

function unescapeXML(text) {
  return text.replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);|&/gi, (whole, name) => {
    if (!name || (name[0] !== '#' && !(name in XML_REFERENCES))) {
      throw new Error(`not a reference: ${whole} in ${text.slice(0, 40)}`);
    }

    if (name[0] !== '#') {
      return XML_REFERENCES[name];
    }

    return String.fromCodePoint(
      name[1] === 'x' ? parseInt(name.slice(2), 16) : Number(name.slice(1)),
    );
  });
}

export function parseXML(text) {
  const root = { name: '', attributes: {}, children: [], text: '' };
  const open = [root];

  XML_TOKEN.lastIndex = 0;

  while (XML_TOKEN.lastIndex < text.length) {
    const at = XML_TOKEN.lastIndex;
    const match = XML_TOKEN.exec(text);

    if (!match) {
      throw new Error(`not well formed at ${at}: ${text.slice(at, at + 40)}`);
    }

    const [, closing, name, attributes, empty, content] = match;
    const parent = open[open.length - 1];

    if (content !== undefined) {
      parent.text += unescapeXML(content);
    } else if (closing) {
      if (open.pop().name !== name) {
        throw new Error(`</${name}> closes another element`);
      }
    } else if (name) {
      const element = { name, attributes: {}, children: [], text: '' };

      for (const [, key, value] of attributes.matchAll(
        /([\w:.-]+)="([^"]*)"/g,
      )) {
        element.attributes[key] = unescapeXML(value);
      }

      parent.children.push(element);

      if (!empty) {
        open.push(element);
      }
    }
  }

  if (open.length !== 1 || root.children.length !== 1) {
    throw new Error('not one root element, or one left open');
  }

  return root.children[0];
}

export function childOf(element, name) {
  return element.children.find((child) => child.name === name);
}
