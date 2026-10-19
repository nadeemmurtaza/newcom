const WebSocket = require('ws');
const { Pool } = require('pg');
const { createClient } = require('redis');
const Y = require('yjs');
const awarenessProtocol = require('y-protocols/awareness');

// Phase 26/58: Real-Time Collaboration (CRDTs + Redis PubSub)
//
// Phase 58 fixes, found by reading this file against the rest of the
// codebase rather than assuming it worked because the Yjs/Redis wiring
// looked right:
//
// 1. Auth was jwt.verify(token, JWT_SECRET) against a hardcoded fallback
//    secret -- but nothing in this entire project ever issues a JWT
//    signed with any secret, let alone this one (nmail's Go side uses
//    opaque Redis session tokens -- user_session:<token> -- not JWTs).
//    No client could ever have obtained a token this would accept. Now
//    verifies the same Redis session nmail's userAuthMiddleware does.
// 2. The WS server hardcoded port 8081 -- the exact port nmail's own
//    HTTP API listens on (main.go's PORT default). Two services bound to
//    the same port on one host is an immediate conflict, not a
//    theoretical one. Now COLLAB_PORT-configurable, default 8090.
// 3. DATABASE_URL's fallback pointed at a database named "esp", which
//    has never existed -- the real one, matching every other service's
//    docker-compose and schema.sql, is "esp_db".
// 4. No authorization check at all: any valid session (for ANY user, in
//    ANY org) could open ANY doc_id. Now checks the connecting user is a
//    member of the org that owns the document before allowing the
//    connection -- documents.org_id's FK target is actually
//    sub_accounts(id), not organizations(id), despite the column name;
//    resolved through sub_accounts.org_id to the real organization.
// 5. Persistence was write-only: ydoc.on('update', ...) saved crdt_state
//    to Postgres, but getYDoc() never read it back -- a brand new Y.Doc
//    was created for every docId on every server restart, silently
//    discarding everything previously saved. Now loads and applies the
//    saved state (if any) the first time a document is opened.
// 6. The initial-sync send called syncProtocol.encodeStateAsUpdate(ydoc),
//    which was never a real export of y-protocols/sync (it only exports
//    message readers/writers for the tagged sync handshake, not a bare
//    encoder) -- this would have thrown "is not a function" on every
//    single connection. Replaced with Y.encodeStateAsUpdate, consistent
//    with the rest of this file sending/receiving raw Yjs update bytes
//    rather than the tagged sync-step protocol.

/**
 * Resolves a nmail session token (the same "user_session:<token>" key
 * userAuthMiddleware in user_auth.go issues and checks) to a user ID.
 * Returns null if the token is missing, expired, or unknown -- never
 * throws, so callers can treat any null the same way as "unauthorized".
 */
async function resolveUserSession(redisClient, token) {
    try {
        const userID = await redisClient.get(`user_session:${token}`);
        return userID || null;
    } catch (err) {
        console.error('Session lookup failed:', err);
        return null;
    }
}

/**
 * True if userID belongs to the same organization that owns docId's
 * document. documents.org_id actually references sub_accounts(id), not
 * organizations(id) -- resolved through that to the real org, then
 * checked against organization_user_permissions the same way
 * hasPermission() in dashboard.go does (any row in that org counts as
 * membership; this isn't trying to enforce per-permission granularity,
 * just "is this person allowed in this org's documents at all").
 */
async function userCanAccessDocument(pool, userID, docId) {
    try {
        const result = await pool.query(
            `SELECT 1 FROM documents d
             JOIN sub_accounts s ON d.org_id = s.id
             JOIN organization_user_permissions p ON p.org_id = s.org_id
             WHERE d.id = $1 AND p.user_id = $2
             LIMIT 1`,
            [docId, userID]
        );
        return result.rowCount > 0;
    } catch (err) {
        console.error('Document access check failed:', err);
        return false;
    }
}

/** Loads a document's previously saved CRDT state, if any. */
async function loadPersistedState(pool, docId) {
    try {
        const result = await pool.query('SELECT crdt_state FROM documents WHERE id = $1', [docId]);
        if (result.rowCount > 0 && result.rows[0].crdt_state) {
            return Buffer.from(result.rows[0].crdt_state, 'base64');
        }
    } catch (err) {
        console.error(`Failed to load persisted state for ${docId}:`, err);
    }
    return null;
}

// Helper to get or create a Yjs document, hydrating it from Postgres on
// first load so a server restart doesn't silently discard prior content.
async function getYDoc(docs, pool, redisSub, docId) {
    if (docs.has(docId)) {
        return docs.get(docId);
    }

    const ydoc = new Y.Doc();
    ydoc.awareness = new awarenessProtocol.Awareness(ydoc);
    docs.set(docId, ydoc);

    const persisted = await loadPersistedState(pool, docId);
    if (persisted) {
        Y.applyUpdate(ydoc, persisted);
    }

    // Listen for Redis Pub/Sub messages from OTHER Node.js servers for this doc
    redisSub.subscribe(`doc:${docId}`, (message) => {
        const update = Buffer.from(message, 'base64');
        Y.applyUpdate(ydoc, update);
    });

    // Persist CRDT state to PostgreSQL on every update
    ydoc.on('update', async () => {
        try {
            const fullState = Y.encodeStateAsUpdate(ydoc);
            const updateBase64 = Buffer.from(fullState).toString('base64');
            await pool.query(
                `UPDATE documents SET crdt_state = $1, updated_at = NOW() WHERE id = $2`,
                [updateBase64, docId]
            );
        } catch (err) {
            console.error(`Failed to persist document ${docId}:`, err);
        }
    });

    return ydoc;
}

function startServer() {
    const pool = new Pool({
        connectionString: process.env.DATABASE_URL || 'postgres://esp_user:esp_password@localhost:5432/esp_db?sslmode=disable'
    });

    const redisPub = createClient({ url: process.env.REDIS_URL || 'redis://localhost:6379' });
    const redisSub = redisPub.duplicate();

    Promise.all([redisPub.connect(), redisSub.connect()]).catch(console.error);

    const COLLAB_PORT = parseInt(process.env.COLLAB_PORT || '8090', 10);
    const wss = new WebSocket.Server({ port: COLLAB_PORT, clientTracking: true });

    // Memory maps for Yjs CRDT Documents
    const docs = new Map();

    // Phase 27: The Post-Ingestion Event Stream (FCM Push Notifications) --
    // one subscription for the whole process, not re-subscribed per document.
    redisSub.subscribe('email_success_stream', (message) => {
        const event = JSON.parse(message);
        console.log(`[Node.js FCM] PUSH NOTIFICATION: New Mail ${event.msg_id} arrived in Mailbox ${event.mailbox_id}!`);
    });

    // Phase 30: Compromised Account Defense (The Kill Switch) -- also a
    // single process-wide subscription.
    redisSub.subscribe('kill_switch_stream', (accountId) => {
        console.log(`[Node.js Kill Switch] REVOKING SESSIONS FOR COMPROMISED ACCOUNT: ${accountId}`);
        wss.clients.forEach(client => {
            if (client.readyState === WebSocket.OPEN && client.user_id === accountId) {
                client.close(1008, 'Account suspended for security reasons');
            }
        });
    });

    wss.on('connection', async (ws, req) => {
    // Phase 58: confirmed live -- auth (resolveUserSession,
    // userCanAccessDocument) and getYDoc are real async DB calls, but this
    // handler used to attach ws.on('message', ...) only after awaiting
    // them. A client that sends its first update immediately on 'open'
    // (exactly what a real editor does) raced the server: its message
    // arrived and was silently dropped before any listener existed to
    // receive it, because nothing buffers an unlistened 'message' event.
    // ws.pause() stops the socket consuming data until resume() is
    // called right after the listener is actually attached below.
    ws.pause();
    try {
        const url = new URL(req.url, `http://${req.headers.host}`);
        const docId = url.searchParams.get('doc_id');
        const token = url.searchParams.get('token');

        if (!docId || !token) {
            ws.close(1008, 'doc_id and token required');
            return;
        }

        const userID = await resolveUserSession(redisPub, token);
        if (!userID) {
            ws.close(1008, 'Unauthorized: invalid or expired session');
            return;
        }

        if (!(await userCanAccessDocument(pool, userID, docId))) {
            ws.close(1008, 'Forbidden: you do not have access to this document');
            return;
        }

        console.log(`[Node.js CRDT] Authenticated User ${userID} connecting to Document ${docId}`);

        const ydoc = await getYDoc(docs, pool, redisSub, docId);

        // Sync Initial State
        const syncState = Y.encodeStateAsUpdate(ydoc);
        ws.send(syncState);

        ws.on('message', (message) => {
            try {
                Y.applyUpdate(ydoc, message);

                // Broadcast to local clients attached to THIS server instance
                wss.clients.forEach(client => {
                    if (client !== ws && client.readyState === WebSocket.OPEN && client.docId === docId) {
                        client.send(message);
                    }
                });

                // Broadcast to OTHER Node.js server instances in the cluster
                const updateBase64 = Buffer.from(message).toString('base64');
                redisPub.publish(`doc:${docId}`, updateBase64);
            } catch (e) {
                console.error('Yjs update error:', e);
            }
        });
        ws.resume();

        // Store metadata on the socket for fast local routing and kill switch
        ws.docId = docId;
        ws.user_id = userID;
    } catch (err) {
        console.error('Auth/Connection error:', err);
        ws.close(1008, 'Unauthorized');
    }
});

    console.log(`[Node.js Microservice] CRDT Collaboration Hub running on ws://localhost:${COLLAB_PORT}`);
}

if (require.main === module) {
    startServer();
}

module.exports = { resolveUserSession, userCanAccessDocument, loadPersistedState, getYDoc, startServer };
