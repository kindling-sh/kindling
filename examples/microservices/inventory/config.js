// config.js — centralised settings.
// Nothing is validated; services log warnings and degrade gracefully.

module.exports = {
  // Hardcoded — the team never got around to making it configurable.
  SERVER_PORT: 3000,

  // Mongo connection string — operator injects as MONGO_URL but the
  // original dev who wrote this called it MONGODB_URI everywhere.
  MONGODB_URI: process.env.MONGODB_URI || "",

  // Redis used exclusively for consuming the order_events queue.
  EVENT_STORE_URL: process.env.EVENT_STORE_URL || "",
};
