// The default mocks of every unit test file. A file that needs other ones
// mocks the module again, or calls vi.unmock for the real one.
import { vi } from 'vitest';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));
