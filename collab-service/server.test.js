const test = require('node:test');
const assert = require('node:assert/strict');
const Y = require('yjs');
const { resolveUserSession, userCanAccessDocument, loadPersistedState, getYDoc } = require('./server');

// Phase 60: real automated tests for the Phase 58 fixes -- each test targets
// a bug that was confirmed live against real Postgres/Redis/WebSocket
// clients (see server.js's top comment). These replace that manual
// verification with something that runs in CI.

test('resolveUserSession returns the user id for a known session token', async () => {
    const redis = { get: async (key) => (key === 'user_session:good-token' ? 'user-42' : null) };
    const userID = await resolveUserSession(redis, 'good-token');
    assert.equal(userID, 'user-42');
});

test('resolveUserSession returns null for an unknown or expired token', async () => {
    const redis = { get: async () => null };
    const userID = await resolveUserSession(redis, 'bogus-token');
    assert.equal(userID, null);
});

test('resolveUserSession returns null (never throws) when Redis errors', async () => {
    const redis = { get: async () => { throw new Error('connection reset'); } };
    const userID = await resolveUserSession(redis, 'any-token');
    assert.equal(userID, null);
});

test('userCanAccessDocument allows a user who is a member of the owning org', async () => {
    const pool = { query: async () => ({ rowCount: 1, rows: [{}] }) };
    const allowed = await userCanAccessDocument(pool, 'user-1', 'doc-1');
    assert.equal(allowed, true);
});

test('userCanAccessDocument denies a user from a different org (the Phase 58 bug)', async () => {
    // Before Phase 58 this check didn't exist at all -- any valid session
    // could open any doc_id regardless of org membership.
    const pool = { query: async () => ({ rowCount: 0, rows: [] }) };
    const allowed = await userCanAccessDocument(pool, 'outsider', 'doc-1');
    assert.equal(allowed, false);
});

test('userCanAccessDocument denies (never throws) when the query fails', async () => {
    const pool = { query: async () => { throw new Error('db down'); } };
    const allowed = await userCanAccessDocument(pool, 'user-1', 'doc-1');
    assert.equal(allowed, false);
});

test('loadPersistedState decodes the stored base64 CRDT state', async () => {
    const raw = Buffer.from('hello-crdt-bytes');
    const pool = { query: async () => ({ rowCount: 1, rows: [{ crdt_state: raw.toString('base64') }] }) };
    const result = await loadPersistedState(pool, 'doc-1');
    assert.ok(Buffer.isBuffer(result));
    assert.equal(result.toString(), 'hello-crdt-bytes');
});

test('loadPersistedState returns null when the document has no saved state yet', async () => {
    const pool = { query: async () => ({ rowCount: 1, rows: [{ crdt_state: null }] }) };
    const result = await loadPersistedState(pool, 'doc-1');
    assert.equal(result, null);
});

test('loadPersistedState returns null (never throws) when the query fails', async () => {
    const pool = { query: async () => { throw new Error('db down'); } };
    const result = await loadPersistedState(pool, 'doc-1');
    assert.equal(result, null);
});

test('getYDoc hydrates a fresh Y.Doc from persisted state -- the write-only persistence bug', async () => {
    // Phase 58's bug: ydoc.on('update', ...) saved crdt_state but getYDoc()
    // never read it back, so a restart silently discarded everything.
    const seed = new Y.Doc();
    seed.getText('body').insert(0, 'previously saved content');
    const savedUpdate = Y.encodeStateAsUpdate(seed);

    const pool = {
        query: async (sql) => {
            if (sql.startsWith('SELECT crdt_state')) {
                return { rowCount: 1, rows: [{ crdt_state: Buffer.from(savedUpdate).toString('base64') }] };
            }
            return { rowCount: 0, rows: [] };
        },
    };
    const redisSub = { subscribe: () => {} };
    const docs = new Map();

    const ydoc = await getYDoc(docs, pool, redisSub, 'doc-restart-test');

    assert.equal(ydoc.getText('body').toString(), 'previously saved content');
});

test('getYDoc returns the same in-memory doc on a second call for the same docId', async () => {
    const pool = { query: async () => ({ rowCount: 0, rows: [] }) };
    const redisSub = { subscribe: () => {} };
    const docs = new Map();

    const first = await getYDoc(docs, pool, redisSub, 'doc-cache-test');
    const second = await getYDoc(docs, pool, redisSub, 'doc-cache-test');

    assert.equal(first, second);
});
