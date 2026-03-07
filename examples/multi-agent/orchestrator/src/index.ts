import express from 'express';
import Redis from 'ioredis';
import { randomUUID } from 'crypto';

const app = express();
app.use(express.json());

const redis = new Redis(process.env.REDIS_URL || 'redis://localhost:6379');
const WORKER_URL = process.env.WORKER_URL || 'http://localhost:3001';

// ── Landing page ────────────────────────────────────────────────

app.get('/', (_req, res) => {
  res.send(`<!DOCTYPE html><html>
<head>
  <title>Research Agents</title>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { font-family: system-ui, -apple-system, sans-serif; background: #0a0e14; color: #c5c8c6; min-height: 100vh; display: flex; align-items: center; justify-content: center; }
    .container { max-width: 640px; width: 100%; padding: 40px 24px; }
    h1 { font-size: 28px; font-weight: 700; color: #e8e8e8; margin-bottom: 8px; }
    .subtitle { color: #6b7280; font-size: 14px; margin-bottom: 32px; }
    .card { background: #12161d; border: 1px solid #1e2530; border-radius: 12px; padding: 24px; margin-bottom: 16px; }
    label { display: block; font-size: 13px; color: #9ca3af; margin-bottom: 8px; }
    input { width: 100%; padding: 10px 14px; background: #1a1f2b; border: 1px solid #2a3140; border-radius: 8px; color: #e8e8e8; font-size: 15px; outline: none; }
    input:focus { border-color: #3b82f6; }
    button { width: 100%; padding: 12px; background: #3b82f6; color: white; border: none; border-radius: 8px; font-size: 15px; font-weight: 600; cursor: pointer; margin-top: 16px; }
    button:hover { background: #2563eb; }
    button:disabled { opacity: 0.5; cursor: not-allowed; }
    pre { background: #0d1117; border: 1px solid #1e2530; border-radius: 8px; padding: 16px; margin-top: 16px; font-size: 13px; overflow-x: auto; white-space: pre-wrap; color: #8b949e; display: none; max-height: 480px; overflow-y: auto; }
    pre.show { display: block; }
    .agents { display: flex; gap: 8px; margin-bottom: 24px; }
    .agent { background: #1a1f2b; border: 1px solid #2a3140; border-radius: 8px; padding: 12px 16px; flex: 1; text-align: center; }
    .agent-icon { font-size: 24px; margin-bottom: 4px; }
    .agent-name { font-size: 12px; color: #9ca3af; }
    .arrow { color: #3b4554; display: flex; align-items: center; font-size: 18px; }
  </style>
</head>
<body>
  <div class="container">
    <h1>Research Agents</h1>
    <p class="subtitle">Multi-agent research pipeline with shared Redis context</p>
    <div class="agents">
      <div class="agent">
        <div class="agent-icon">🎯</div>
        <div class="agent-name">Orchestrator</div>
      </div>
      <div class="arrow">→</div>
      <div class="agent">
        <div class="agent-icon">📚</div>
        <div class="agent-name">Researcher</div>
      </div>
      <div class="arrow">→</div>
      <div class="agent">
        <div class="agent-icon">🔍</div>
        <div class="agent-name">Critic</div>
      </div>
    </div>
    <div class="card">
      <label for="topic">Research Topic</label>
      <input id="topic" placeholder="try: artificial intelligence, space, climate, quantum" />
      <button id="go" onclick="doResearch()">Run Research Pipeline</button>
    </div>
    <pre id="result"></pre>
  </div>
  <script>
    async function doResearch() {
      const btn = document.getElementById('go');
      const pre = document.getElementById('result');
      const topic = document.getElementById('topic').value.trim();
      if (!topic) return;
      btn.disabled = true;
      btn.textContent = 'Dispatching agents…';
      pre.className = 'show';
      pre.textContent = 'Running pipeline…';
      try {
        const res = await fetch('/research', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ topic }),
        });
        const data = await res.json();
        pre.textContent = JSON.stringify(data, null, 2);
      } catch (e) {
        pre.textContent = 'Error: ' + e.message;
      }
      btn.disabled = false;
      btn.textContent = 'Run Research Pipeline';
    }
    document.getElementById('topic').addEventListener('keydown', (e) => {
      if (e.key === 'Enter') doResearch();
    });
  </script>
</body></html>`);
});

// ── Health ───────────────────────────────────────────────────────

app.get('/health', (_req, res) => {
  res.json({ status: 'ok', service: 'orchestrator', uptime: process.uptime() });
});

// ── Research pipeline ───────────────────────────────────────────

app.post('/research', async (req, res) => {
  const { topic } = req.body;
  if (!topic) {
    res.status(400).json({ error: 'topic is required' });
    return;
  }

  const requestId = randomUUID();
  const startTime = Date.now();

  // Store request context in Redis — all agents can see this
  await redis.set(
    `request:${requestId}`,
    JSON.stringify({ topic, ts: startTime }),
    'EX', 300,
  );

  try {
    // Step 1: Dispatch to researcher agent
    const researchRes = await fetch(`${WORKER_URL}/agent/researcher`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ requestId, topic }),
    });
    const research = await researchRes.json() as Record<string, unknown>;

    // Step 2: Dispatch to critic agent (reads researcher's cached findings)
    const criticRes = await fetch(`${WORKER_URL}/agent/critic`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ requestId, topic }),
    });
    const critique = await criticRes.json() as Record<string, unknown>;

    // Step 3: Read the final review from Redis
    const reviewRaw = await redis.get(`review:${requestId}`);
    const review = reviewRaw ? JSON.parse(reviewRaw) : null;

    res.json({
      requestId,
      topic,
      pipeline: { researcher: research, critic: critique },
      review,
      duration_ms: Date.now() - startTime,
    });
  } catch (err: unknown) {
    const message = err instanceof Error ? err.message : String(err);
    res.status(502).json({
      error: 'Agent pipeline failed',
      detail: message,
      requestId,
    });
  }
});

// ── Lookup a past result ────────────────────────────────────────

app.get('/research/:id', async (req, res) => {
  const review = await redis.get(`review:${req.params.id}`);
  if (!review) {
    res.status(404).json({ error: 'not found' });
    return;
  }
  res.json(JSON.parse(review));
});

// ── Start ───────────────────────────────────────────────────────

const PORT = parseInt(process.env.PORT || '3000');
app.listen(PORT, '0.0.0.0', () => {
  console.log(`🎯 Orchestrator listening on :${PORT}`);
  console.log(`   Worker URL: ${WORKER_URL}`);
});
