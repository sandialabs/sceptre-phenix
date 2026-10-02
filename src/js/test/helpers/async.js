// Waits for the promise callbacks queued so far, and the ones they queue, to
// run: a request answered by a mock has been handled once this resolves.
export const flush = () => new Promise((resolve) => setTimeout(resolve));

// Has the mock `get` (such as axios.get) wait on each call until the test
// answers it. Returns the calls in order, each its URL with the resolve and
// reject of its answer.
export function deferredGets(get) {
  const calls = [];
  get.mockImplementation((url) => {
    const answer = Promise.withResolvers();
    calls.push({ url, ...answer });
    return answer.promise;
  });
  return calls;
}
