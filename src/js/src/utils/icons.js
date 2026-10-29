// Icons of our own, in Font Awesome's icon definition format so b-icon and
// font-awesome-icon draw them like the solid set: add them to the library in
// main.js and use them by name.

// A shark fin cutting through water, after Wireshark's logo: the packet
// capture icon.
export const faSharkFin = {
  prefix: 'fas',
  iconName: 'shark-fin',
  icon: [
    512,
    512,
    [],
    '',
    'M104 424C152 256 240 104 384 24C340 160 352 296 464 424Z' +
      'M0 384Q64 336 128 384T256 384T384 384T512 384V456' +
      'Q448 504 384 456T256 456T128 456T0 456Z',
  ],
};

// A penguin, for Linux guests: head and body with a hollow belly and eyes,
// drawn against the outline's winding so they show through.
export const faLinux = {
  prefix: 'fas',
  iconName: 'linux',
  icon: [
    512,
    512,
    [],
    '',
    'M166 130A90 90 0 1 1 346 130A90 90 0 1 1 166 130Z' +
      'M204 120A18 18 0 1 0 240 120A18 18 0 1 0 204 120Z' +
      'M272 120A18 18 0 1 0 308 120A18 18 0 1 0 272 120Z' +
      'M106 320A150 170 0 1 1 406 320A150 170 0 1 1 106 320Z' +
      'M161 340A95 125 0 1 0 351 340A95 125 0 1 0 161 340Z' +
      'M106 488A70 24 0 1 1 246 488A70 24 0 1 1 106 488Z' +
      'M266 488A70 24 0 1 1 406 488A70 24 0 1 1 266 488Z',
  ],
};

// Four panes of a window, for Windows guests.
export const faWindows = {
  prefix: 'fas',
  iconName: 'windows',
  icon: [
    512,
    512,
    [],
    '',
    'M32 32H246V246H32Z' +
      'M266 32H480V246H266Z' +
      'M32 266H246V480H32Z' +
      'M266 266H480V480H266Z',
  ],
};
