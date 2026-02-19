/**
 * inventory service — Fastify + MongoDB + Redis
 *
 * Serves product stock levels out of MongoDB and runs a background
 * BRPOP loop that consumes order_events from Redis, decrementing
 * stock for each order.
 *
 * Health endpoint: GET /healthcheck
 * Products:        GET /inventory
 * Single product:  GET /inventory/:sku
 */

"use strict";

const Fastify = require("fastify");
const { MongoClient } = require("mongodb");
const Redis = require("ioredis");
const cfg = require("./config");

const app = Fastify({ logger: true });

let db = null;
let redis = null;

// ── Mongo ────────────────────────────────────────────────────────

async function initMongo() {
  if (!cfg.MONGODB_URI) return;
  const client = new MongoClient(cfg.MONGODB_URI);
  await client.connect();
  db = client.db("inventory");
  await seed();
  app.log.info("mongo connected");
}

async function seed() {
  const col = db.collection("products");
  if ((await col.countDocuments()) > 0) return;
  await col.insertMany([
    { sku: "widget-a", name: "Widget A", stock: 100 },
    { sku: "widget-b", name: "Widget B", stock: 250 },
    { sku: "gadget-x", name: "Gadget X", stock: 50 },
  ]);
  app.log.info("seeded 3 products");
}

// ── Redis event consumer ─────────────────────────────────────────

async function initRedis() {
  if (!cfg.EVENT_STORE_URL) return;
  redis = new Redis(cfg.EVENT_STORE_URL);
  app.log.info("redis connected — consuming order_events");
  consumeLoop(); // fire-and-forget
}

async function consumeLoop() {
  while (true) {
    try {
      const msg = await redis.brpop("order_events", 0);
      if (!msg) continue;
      const evt = JSON.parse(msg[1]);
      if (db && evt.product && evt.quantity) {
        await db
          .collection("products")
          .updateOne({ sku: evt.product }, { $inc: { stock: -evt.quantity } });
        app.log.info(`stock adjusted: ${evt.product} -${evt.quantity}`);
      }
    } catch (err) {
      app.log.error(err, "event consumer error");
      await new Promise((r) => setTimeout(r, 2000));
    }
  }
}

// ── Routes ───────────────────────────────────────────────────────

app.get("/healthcheck", async () => ({ ok: true }));

app.get("/inventory", async (_req, reply) => {
  if (!db) return reply.code(503).send({ error: "database offline" });
  const products = await db
    .collection("products")
    .find({}, { projection: { _id: 0 } })
    .toArray();
  return { products };
});

app.get("/inventory/:sku", async (req, reply) => {
  if (!db) return reply.code(503).send({ error: "database offline" });
  const p = await db
    .collection("products")
    .findOne({ sku: req.params.sku }, { projection: { _id: 0 } });
  if (!p) return reply.code(404).send({ error: "not found" });
  return p;
});

app.get("/status", async () => {
  const out = { service: "inventory", time: new Date().toISOString() };
  if (db) {
    try {
      await db.command({ ping: 1 });
      out.mongodb = "connected";
    } catch (e) {
      out.mongodb = `error: ${e.message}`;
    }
  } else {
    out.mongodb = "not configured";
  }
  if (redis) {
    try {
      await redis.ping();
      out.redis = "connected";
    } catch (e) {
      out.redis = `error: ${e.message}`;
    }
  } else {
    out.redis = "not configured";
  }
  return out;
});

// ── Start ────────────────────────────────────────────────────────

async function main() {
  await initMongo().catch((e) =>
    app.log.warn(`mongo unavailable: ${e.message}`)
  );
  await initRedis().catch((e) =>
    app.log.warn(`redis unavailable: ${e.message}`)
  );
  await app.listen({ port: cfg.SERVER_PORT, host: "0.0.0.0" });
}

main();
