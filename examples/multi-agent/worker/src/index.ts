import express from 'express';
import Redis from 'ioredis';

const app = express();
app.use(express.json());

const redis = new Redis(process.env.REDIS_URL || 'redis://localhost:6379');

// ── Knowledge base ──────────────────────────────────────────────

interface TopicData {
  field: string;
  facts: string[];
}

const KNOWLEDGE: Record<string, TopicData> = {
  ai: {
    field: 'Artificial Intelligence',
    facts: [
      'Transformer architecture, introduced in 2017, became the foundation for modern LLMs',
      'Multi-agent systems decompose complex tasks into specialized subtasks for better results',
      'RLHF improved alignment between model outputs and human preferences',
      'Mixture-of-experts architectures allow scaling model capacity without proportional compute',
      'Tool-use capabilities let agents interact with external systems and APIs',
    ],
  },
  space: {
    field: 'Space Exploration',
    facts: [
      'James Webb Space Telescope operates at the L2 Lagrange point, 1.5M km from Earth',
      'SpaceX Starship is the largest and most powerful rocket ever built',
      'Mars Ingenuity helicopter completed 72 flights, far exceeding its 5-flight plan',
      'The Artemis program aims to establish a sustained lunar presence',
      'Europa Clipper will perform 49 flybys of Jupiter\'s icy moon',
    ],
  },
  climate: {
    field: 'Climate Science',
    facts: [
      'Global surface temperature has risen approximately 1.1°C since pre-industrial times',
      'Arctic sea ice extent is declining at roughly 13% per decade',
      'Renewable energy capacity additions surpassed fossil fuels globally in 2023',
      'Ocean heat content reached record levels, driving more intense weather events',
      'Direct air carbon capture remains below 0.01% of annual global emissions',
    ],
  },
  quantum: {
    field: 'Quantum Computing',
    facts: [
      'Quantum supremacy was demonstrated by Google\'s Sycamore processor in 2019',
      'Error correction remains the primary barrier to practical quantum computing',
      'Topological qubits promise inherent error resistance through braided anyons',
      'Quantum key distribution enables theoretically unbreakable encryption',
      'Hybrid classical-quantum algorithms show near-term practical promise',
    ],
  },
};

function findTopic(query: string): TopicData {
  const q = query.toLowerCase();
  for (const [key, data] of Object.entries(KNOWLEDGE)) {
    if (q.includes(key)) return data;
  }
  return {
    field: 'General Research',
    facts: [
      'This is a broad topic with many dimensions worth exploring',
      'Interdisciplinary approaches often yield the most interesting insights',
      'Recent advances have accelerated progress in this area significantly',
    ],
  };
}

// ── Health ───────────────────────────────────────────────────────

app.get('/health', (_req, res) => {
  res.json({ status: 'ok', service: 'worker', agents: ['researcher', 'critic'], uptime: process.uptime() });
});

// ── Researcher agent ────────────────────────────────────────────
//
// Generates findings for a topic and caches them in Redis so
// downstream agents (critic) can build on the research.

app.post('/agent/researcher', async (req, res) => {
  const { requestId, topic } = req.body;
  const topicData = findTopic(topic);

  const findings = {
    field: topicData.field,
    facts: topicData.facts,
    confidence: Math.round((0.72 + Math.random() * 0.23) * 100) / 100,
    analyzed_at: new Date().toISOString(),
  };

  // ⚠ Respond to the orchestrator immediately, THEN persist to
  //   Redis. This is a realistic pattern — agents often do
  //   post-response work (indexing, logging, vector upserts).
  //
  //   The problem: the orchestrator gets this response and fires
  //   off the critic request before the Redis write below lands.
  res.json({ agent: 'researcher', status: 'complete', requestId });

  // Simulate post-response indexing work
  const indexDelay = parseInt(process.env.INDEX_DELAY_MS || '10');
  await new Promise(resolve => setTimeout(resolve, indexDelay));

  await redis.set(
    `research:${requestId}`,
    JSON.stringify(findings),
    'EX', 300,
  );
});

// ── Critic agent ────────────────────────────────────────────────
//
// Reads the researcher's cached findings from Redis and produces
// a critical assessment of the research quality.

app.post('/agent/critic', async (req, res) => {
  const { requestId, topic } = req.body;

  // Read the researcher's findings from Redis
  const cached = await redis.get(`research:${requestId}`);

  // Determine cache status
  // 🐛 BUG: redis.get() returns null on a cache miss, never undefined.
  //    Since null !== undefined is always true, source is always 'cache'
  //    even when nothing was found. The correct check is: cached !== null
  const source = cached !== undefined ? 'cache' : 'miss';

  const findings = cached ? JSON.parse(cached) : null;

  const assessment = findings
    ? `Research on "${findings.field}" covers ${findings.facts.length} key points ` +
      `with ${(findings.confidence * 100).toFixed(0)}% confidence. ` +
      `The analysis is well-structured but could benefit from additional primary sources.`
    : `No research findings available for "${topic}". The researcher agent may not have ` +
      `completed in time. This indicates a pipeline synchronization issue.`;

  const critique = {
    agent: 'critic',
    requestId,
    source,            // Visual cue: says 'cache' even when findings is null
    findings_received: findings !== null,
    assessment,
    reviewed_at: new Date().toISOString(),
  };

  // Store the combined review for the orchestrator to pick up
  await redis.set(`review:${requestId}`, JSON.stringify(critique), 'EX', 300);

  res.json(critique);
});

// ── Start ───────────────────────────────────────────────────────

const PORT = parseInt(process.env.PORT || '3001');
app.listen(PORT, '0.0.0.0', () => {
  console.log(`🔬 Worker agents listening on :${PORT}`);
  console.log(`   Agents: researcher, critic`);
});
