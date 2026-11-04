const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const script = fs.readFileSync(path.join(__dirname, 'app.js'), 'utf8');

function homepage(revision = '9007199254740992') {
  const documentEvents = {};
  const windowEvents = {};
  const state = { requests: 0, reloads: 0, warnings: 0, next: revision };
  const document = {
    body: { dataset: { page: 'homepage', currentRevision: revision } },
    visibilityState: 'visible',
    querySelectorAll: () => [],
    addEventListener: (name, callback) => { documentEvents[name] = callback; },
  };
  vm.runInNewContext(script, {
    document,
    window: { addEventListener: (name, callback) => { windowEvents[name] = callback; } },
    location: { reload: () => { state.reloads++; } },
    console: { warn: () => { state.warnings++; } },
    setInterval: (callback, delay) => {
      state.poll = callback;
      assert.equal(delay, 2000);
    },
    fetch: async (url, options) => {
      state.requests++;
      assert.equal(url, '/homepage/version');
      assert.equal(options.cache, 'no-store');
      if (state.error) throw state.error;
      return { ok: true, text: async () => state.next };
    },
  });
  documentEvents.DOMContentLoaded();
  return { state, document, documentEvents, windowEvents };
}

test('empty homepage polls without reloading unchanged data', async () => {
  const { state } = homepage();
  await state.poll();
  assert.equal(state.requests, 1);
  assert.equal(state.reloads, 0);
});

for (const event of ['focus', 'pageshow', 'visibilitychange']) {
  test(`returning via ${event} checks changes without losing integer precision`, async () => {
    const { state, documentEvents, windowEvents } = homepage();
    state.next = '9007199254740993';
    await (documentEvents[event] || windowEvents[event])();
    assert.equal(state.reloads, 1);
    await state.poll();
    assert.equal(state.requests, 1);
  });
}

test('hidden page waits until visible and avoids overlapping checks', async () => {
  const { state, document, documentEvents, windowEvents } = homepage();
  document.visibilityState = 'hidden';
  await state.poll();
  assert.equal(state.requests, 0);
  document.visibilityState = 'visible';
  await Promise.all([documentEvents.visibilitychange(), windowEvents.focus()]);
  assert.equal(state.requests, 1);
});

test('a lower revision after restart also triggers reload', async () => {
  const { state } = homepage();
  state.next = '1';
  await state.poll();
  assert.equal(state.reloads, 1);
});

test('failed and invalid checks warn and can be retried', async () => {
  const { state } = homepage();
  state.error = new Error('offline');
  await state.poll();
  state.error = null;
  state.next = '<html>error</html>';
  await state.poll();
  assert.equal(state.warnings, 2);
  assert.equal(state.reloads, 0);
  state.next = '1';
  await state.poll();
  assert.equal(state.reloads, 1);
});
