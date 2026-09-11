const releaseVersionPattern =
  /^v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/;
const upstreamRepo = 'sandialabs/sceptre-phenix';
const defaultRepo = 'repo not set';

export function formatVersion({ commit, tag, buildDate, repo }) {
  let source = `Branch ${tag}`;

  if (releaseVersionPattern.test(tag)) {
    source = `Version ${tag}`;
  } else if (repo && repo !== defaultRepo && repo !== upstreamRepo) {
    source = `Branch ${tag} [${repo.toLowerCase()}]`;
  }

  return `${source} (commit ${commit}, built on ${buildDate})`;
}
