const WebSocket = require('ws');
const { Pool } = require('pg');
const { createClient } = require('redis');
const jwt = require('jsonwebtoken');
const Y = require('yjs');
const syncProtocol = require('y-protocols/sync');
const awarenessProtocol = require('y-protocols/awareness');

// Phase 26: Commercial-Grade Real-Time Collaboration (CRDTs + Redis PubSub + JWT)

// 1. Database Connection
const pool = new Pool({
    connectionString: process.env.DATABASE_URL || 'postgres://postgres:postgres@localhost:5432/esp?sslmode=disable'
});

// 2. Redis Pub/Sub for Horizontal Scaling (Multiple Node.js instances)
const redisPub = createClient({ url: process.env.REDIS_URL || 'redis://localhost:6379' });
const redisSub = redisPub.duplicate();

Promise.all([redisPub.connect(), redisSub.connect()]).catch(console.error);

// 3. JWT Authentication Key
const JWT_SECRET = process.env.JWT_SECRET || 'super-secret-commercial-key-123';

// 4. WebSocket Server
const wss = new WebSocket.Server({ port: 8081, clientTracking: true });

// Memory maps for Yjs CRDT Documents
const docs = new Map();

// Helper to get or create a Yjs document
function getYDoc(docId) {
    if (!docs.has(docId)) {
        const ydoc = new Y.Doc();
        ydoc.awareness = new awarenessProtocol.Awareness(ydoc);
        docs.set(docId, ydoc);
        
        // Listen for Redis Pub/Sub messages from OTHER Node.js servers for this doc
        redisSub.subscribe(`doc:${docId}`, (message) => {
            const update = Buffer.from(message, 'base64');
            Y.applyUpdate(ydoc, update);
        });

        // Phase 27: The Post-Ingestion Event Stream (FCM Push Notifications)
        redisSub.subscribe('email_success_stream', (message) => {
            const event = JSON.parse(message);
            console.log(`[Node.js FCM] PUSH NOTIFICATION: New Mail ${event.msg_id} arrived in Mailbox ${event.mailbox_id}!`);
        });

        // Phase 30: Compromised Account Defense (The Kill Switch)
        redisSub.subscribe('kill_switch_stream', (accountId) => {
            console.log(`[Node.js Kill Switch] 🚨 REVOKING SESSIONS FOR COMPROMISED ACCOUNT: ${accountId}`);
            // Force terminate all websockets for this user globally
            wss.clients.forEach(client => {
                if (client.readyState === WebSocket.OPEN && client.user_id === accountId) {
                    client.close(1008, "Account suspended for security reasons");
                }
            });
        });

        // Persist CRDT state to PostgreSQL periodically
        ydoc.on('update', async (update) => {
            try {
                // In a true commercial setup, you store the binary Yjs updates in a BLOB
                const updateBase64 = Buffer.from(update).toString('base64');
                await pool.query(
                    `UPDATE documents SET crdt_state = $1, updated_at = NOW() WHERE id = $2`,
                    [updateBase64, docId]
                );
            } catch (err) {
                // Ignore for now, requires crdt_state column in DB
            }
        });
    }
    return docs.get(docId);
}

wss.on('connection', (ws, req) => {
    try {
        const url = new URL(req.url, `http://${req.headers.host}`);
        const docId = url.searchParams.get('doc_id');
        const token = url.searchParams.get('token');

        if (!docId || !token) {
            ws.close(1008, "doc_id and token required");
            return;
        }

        // Commercial Logic: JWT Authentication
        const user = jwt.verify(token, JWT_SECRET);
        console.log(`[Node.js CRDT] Authenticated User ${user.id} connecting to Document ${docId}`);

        const ydoc = getYDoc(docId);
        
        // Sync Initial State
        const syncState = syncProtocol.encodeStateAsUpdate(ydoc);
        ws.send(syncState);

        ws.on('message', (message) => {
            // Commercial Logic: Apply Yjs CRDT Update
            try {
                // Apply update to local Yjs Document
                Y.applyUpdate(ydoc, message);
                
                // Broadcast to local clients attached to THIS server instance
                wss.clients.forEach(client => {
                    if (client !== ws && client.readyState === WebSocket.OPEN && client.docId === docId) {
                        client.send(message);
                    }
                });

                // Commercial Logic: Redis Pub/Sub for Horizontal Scaling
                // Broadcast to OTHER Node.js server instances in the cluster
                const updateBase64 = Buffer.from(message).toString('base64');
                redisPub.publish(`doc:${docId}`, updateBase64);
                
            } catch (e) {
                console.error("Yjs update error:", e);
            }
        });

        // Store metadata on the socket for fast local routing and kill switch
        ws.docId = docId;
        ws.user_id = user.id;

    } catch (err) {
        console.error("Auth/Connection error:", err);
        ws.close(1008, "Unauthorized");
    }
});

console.log("[Node.js Microservice] Commercial CRDT Collaboration Hub running on ws://localhost:8081");
