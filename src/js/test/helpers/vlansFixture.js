// A GET /vlans body with every issue kind, from the Go vlan.Merge of a sample
// cluster (TestFrontendFixtureMatchesMerge in api/vlan writes and checks it).
import body from './vlansFixture.json';

// A new copy of the body, which a test may change.
export const vlansFixture = () => structuredClone(body);
