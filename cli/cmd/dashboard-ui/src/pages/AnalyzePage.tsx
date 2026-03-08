import { useState } from 'react';
import { apiPost, streamGenerate, streamGitPush } from '../api';
import type { GenerateResult, GitPushResult } from '../api';
import { ActionButton, useToast } from './actions';
import { PathAutocomplete } from './PathAutocomplete';

/* ── Types ─────────────────────────────────────────────────── */

interface AnalyzeCheck {
  status: 'pass' | 'warn' | 'fail' | 'info';
  message: string;
  fix?: string;
}

interface AnalyzeCategory {
  category: string;
  checks: AnalyzeCheck[];
}

interface AnalyzeResult {
  ok: boolean;
  error?: string;
  repoPath?: string;
  language?: string;
  categories?: AnalyzeCategory[];
  summary?: { pass: number; warn: number; fail: number; ready: boolean };
  existingWorkflowPath?: string;
  existingWorkflow?: string;
}

/* ── Component ─────────────────────────────────────────────── */

export function AnalyzePage() {
  const { toast } = useToast();

  // ── analyze state ──────────────────────────
  const [analyzing, setAnalyzing] = useState(false);
  const [analyzeResult, setAnalyzeResult] = useState<AnalyzeResult | null>(null);
  const [repoPath, setRepoPath] = useState('');

  // ── generate state ─────────────────────────
  const [showGenerate, setShowGenerate] = useState(false);
  const [generateRunning, setGenerateRunning] = useState(false);
  const [generateMessages, setGenerateMessages] = useState<string[]>([]);
  const [generateResult, setGenerateResult] = useState<GenerateResult | null>(null);
  const [generateForm, setGenerateForm] = useState({
    apiKey: '', provider: 'openai', model: '', ciProvider: 'github', branch: '',
  });

  // ── push state ─────────────────────────────
  const [pushRunning, setPushRunning] = useState(false);
  const [pushMessages, setPushMessages] = useState<string[]>([]);
  const [pushResult, setPushResult] = useState<GitPushResult | null>(null);
  const [commitMessage, setCommitMessage] = useState('');

  async function handleAnalyze() {
    setAnalyzing(true);
    setAnalyzeResult(null);
    setShowGenerate(false);
    setGenerateResult(null);
    setGenerateMessages([]);
    setPushResult(null);
    setPushMessages([]);
    setCommitMessage('');
    try {
      const res = await apiPost('/api/analyze', { repoPath: repoPath || undefined });
      setAnalyzeResult(res as unknown as AnalyzeResult);
    } catch {
      setAnalyzeResult({ ok: false, error: 'Request failed' });
    }
    setAnalyzing(false);
  }

  async function handleGenerate() {
    setGenerateRunning(true);
    setGenerateMessages([]);
    setGenerateResult(null);
    const result = await streamGenerate(
      {
        apiKey: generateForm.apiKey,
        repoPath: repoPath || undefined,
        provider: generateForm.provider || undefined,
        model: generateForm.model || undefined,
        ciProvider: generateForm.ciProvider || undefined,
        branch: generateForm.branch || undefined,
      },
      (msg) => setGenerateMessages((m) => [...m, msg]),
    );
    setGenerateResult(result);
    setGenerateRunning(false);
    if (result.ok) toast(result.output || 'Workflow generated', 'success');
    else toast(result.error || 'Generation failed', 'error');
  }

  async function handlePush() {
    if (!generateResult?.path) return;
    setPushRunning(true);
    setPushMessages([]);
    setPushResult(null);
    const result = await streamGitPush(
      {
        repoPath: repoPath || undefined,
        files: [generateResult.path],
        message: commitMessage || `Add kindling CI workflow`,
        branch: generateForm.branch || undefined,
      },
      (msg) => setPushMessages((m) => [...m, msg]),
    );
    setPushResult(result);
    setPushRunning(false);
    if (result.ok) toast(result.output || 'Pushed successfully', 'success');
    else toast(result.error || 'Push failed', 'error');
  }

  const summary = analyzeResult?.summary;
  const ready = summary?.ready ?? false;
  const hasExistingWorkflow = !!analyzeResult?.existingWorkflow;

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Analyze &amp; Generate</h1>
          <p className="page-subtitle">Check your repo's readiness, then generate a CI workflow with AI</p>
        </div>
      </div>

      {/* ── Step 1: Analyze ────────────────── */}
      <div className="analyze-step">
        <div className="analyze-step-header">
          <span className="analyze-step-number">1</span>
          <div>
            <h2 className="analyze-step-title">Analyze Repository</h2>
            <p className="analyze-step-desc">Scan your repo for Dockerfiles, dependencies, secrets, and Kaniko compatibility — no API key needed.</p>
          </div>
        </div>

        <div className="analyze-input-row">
          <PathAutocomplete
            value={repoPath}
            onChange={setRepoPath}
            placeholder="Repo path (leave blank for auto-detect)"
            style={{ flex: 1 }}
          />
          <ActionButton
            icon={analyzing ? '⏳' : '🔍'}
            label={analyzing ? 'Analyzing…' : 'Analyze'}
            onClick={handleAnalyze}
            disabled={analyzing}
            primary
          />
        </div>
      </div>

      {/* ── Analyze Results ────────────────── */}
      {analyzeResult && (
        <div className="analyze-results">
          {!analyzeResult.ok ? (
            <div className="analyze-error">
              <span>❌</span> {analyzeResult.error}
            </div>
          ) : (
            <>
              {/* Summary bar */}
              <div className="analyze-summary-bar">
                <div className="analyze-summary-pills">
                  {(summary?.pass ?? 0) > 0 && <span className="analyze-pill analyze-pill-pass">✅ {summary!.pass} passed</span>}
                  {(summary?.warn ?? 0) > 0 && <span className="analyze-pill analyze-pill-warn">⚠️ {summary!.warn} warnings</span>}
                  {(summary?.fail ?? 0) > 0 && <span className="analyze-pill analyze-pill-fail">❌ {summary!.fail} blockers</span>}
                </div>
                <div className="analyze-summary-verdict">
                  {ready ? (
                    <span className="analyze-verdict-ready">✅ Ready for CI generation</span>
                  ) : (
                    <span className="analyze-verdict-blocked">❌ Fix blockers before generating</span>
                  )}
                </div>
              </div>

              {/* Info strip */}
              <div className="analyze-meta">
                {analyzeResult.repoPath && <span className="tag">📂 {analyzeResult.repoPath}</span>}
                {analyzeResult.language && <span className="tag tag-purple">🔤 {analyzeResult.language}</span>}
              </div>

              {/* Category cards */}
              <div className="analyze-categories">
                {analyzeResult.categories?.map((cat) => (
                  <AnalyzeCategoryCard key={cat.category} category={cat} />
                ))}
              </div>

              {/* Existing workflow viewer */}
              {analyzeResult.existingWorkflow && (
                <details className="analyze-workflow-viewer">
                  <summary>
                    <span>📄</span> View existing workflow — <code>{analyzeResult.existingWorkflowPath}</code>
                  </summary>
                  <pre className="log-output" style={{ marginTop: 8, maxHeight: 400, overflow: 'auto', fontSize: 11 }}>
                    {analyzeResult.existingWorkflow}
                  </pre>
                </details>
              )}
            </>
          )}
        </div>
      )}

      {/* ── Step 2: Generate ───────────────── */}
      {analyzeResult?.ok && (
        <div className="analyze-step" style={{ marginTop: 24 }}>
          <div className="analyze-step-header">
            <span className={`analyze-step-number ${ready ? 'ready' : 'blocked'}`}>2</span>
            <div>
              <h2 className="analyze-step-title">{hasExistingWorkflow ? 'Re-generate CI Workflow' : 'Generate CI Workflow'}</h2>
              <p className="analyze-step-desc">
                {!ready
                  ? 'Fix the blockers above first, then generate your CI workflow.'
                  : hasExistingWorkflow
                    ? `Existing workflow found at ${analyzeResult.existingWorkflowPath}. Re-generate to overwrite it with AI.`
                    : 'Your repo looks good. Provide an API key to generate a CI workflow.'}
              </p>
            </div>
          </div>

          {!showGenerate && !generateResult && (
            <ActionButton
              icon="⚙️"
              label="Configure Generation"
              onClick={() => setShowGenerate(true)}
              disabled={!ready}
              primary
            />
          )}

          {showGenerate && !generateRunning && !generateResult && (
            <div className="analyze-generate-form">
              <div className="form-row">
                <div className="form-field">
                  <label className="form-label">API Key <span style={{ color: 'var(--danger)' }}>*</span></label>
                  <input
                    className="form-input"
                    type="password"
                    placeholder="sk-..."
                    value={generateForm.apiKey}
                    onChange={(e) => setGenerateForm({ ...generateForm, apiKey: e.target.value })}
                  />
                </div>
              </div>

              <div className="form-row form-row-2col">
                <div className="form-field">
                  <label className="form-label">AI Provider</label>
                  <select
                    className="form-input"
                    value={generateForm.provider}
                    onChange={(e) => setGenerateForm({ ...generateForm, provider: e.target.value })}
                  >
                    <option value="openai">OpenAI</option>
                    <option value="anthropic">Anthropic</option>
                  </select>
                </div>
                <div className="form-field">
                  <label className="form-label">Model <span className="form-hint">(optional)</span></label>
                  <input
                    className="form-input"
                    placeholder={generateForm.provider === 'anthropic' ? 'claude-sonnet-4-20250514' : 'o3'}
                    value={generateForm.model}
                    onChange={(e) => setGenerateForm({ ...generateForm, model: e.target.value })}
                  />
                </div>
              </div>

              <div className="form-row form-row-2col">
                <div className="form-field">
                  <label className="form-label">CI Provider</label>
                  <select
                    className="form-input"
                    value={generateForm.ciProvider}
                    onChange={(e) => setGenerateForm({ ...generateForm, ciProvider: e.target.value })}
                  >
                    <option value="github">GitHub Actions</option>
                    <option value="gitlab">GitLab CI</option>
                  </select>
                </div>
                <div className="form-field">
                  <label className="form-label">Branch <span className="form-hint">(auto-detect)</span></label>
                  <input
                    className="form-input"
                    placeholder="main"
                    value={generateForm.branch}
                    onChange={(e) => setGenerateForm({ ...generateForm, branch: e.target.value })}
                  />
                </div>
              </div>

              <div className="form-actions">
                <ActionButton
                  icon="⚡"
                  label="Generate"
                  onClick={handleGenerate}
                  disabled={!generateForm.apiKey}
                  primary
                />
                <button className="btn" onClick={() => setShowGenerate(false)}>Cancel</button>
              </div>
            </div>
          )}

          {/* Generation progress */}
          {(generateMessages.length > 0 || generateResult) && (
            <div className="analyze-generate-output">
              {generateMessages.length > 0 && (
                <div className="log-viewer" style={{ marginBottom: 12 }}>
                  <pre className="log-output">
                    {generateMessages.map((m, i) => <div key={i}>{m}</div>)}
                  </pre>
                </div>
              )}
              {generateRunning && generateMessages.length === 0 && (
                <p style={{ color: 'var(--text-tertiary)' }}>Scanning repository…</p>
              )}
              {generateResult && (
                <div className={`analyze-generate-result ${generateResult.ok ? 'success' : 'error'}`}>
                  <span>{generateResult.ok ? '✅' : '❌'}</span>
                  <span>{generateResult.output || generateResult.error}</span>
                </div>
              )}
              {generateResult?.workflow && (
                <details style={{ marginTop: 8 }}>
                  <summary style={{ cursor: 'pointer', color: 'var(--text-secondary)', fontSize: 13 }}>
                    View generated workflow
                  </summary>
                  <pre className="log-output" style={{ marginTop: 8, maxHeight: 400, overflow: 'auto', fontSize: 11 }}>
                    {generateResult.workflow}
                  </pre>
                </details>
              )}
              {generateResult?.path && (
                <p style={{ fontSize: 12, color: 'var(--text-tertiary)', marginTop: 8 }}>
                  Written to: <code>{generateResult.path}</code>
                </p>
              )}
            </div>
          )}
        </div>
      )}

      {/* ── Step 3: Commit & Push ──────────── */}
      {generateResult?.ok && generateResult?.path && (
        <div className="analyze-step" style={{ marginTop: 24 }}>
          <div className="analyze-step-header">
            <span className="analyze-step-number ready">3</span>
            <div>
              <h2 className="analyze-step-title">Commit &amp; Push</h2>
              <p className="analyze-step-desc">
                Commit the generated workflow to git and push to trigger your first CI build.
              </p>
            </div>
          </div>

          {!pushRunning && !pushResult && (
            <div className="analyze-generate-form">
              <div className="form-row">
                <div className="form-field">
                  <label className="form-label">Commit Message</label>
                  <input
                    className="form-input"
                    placeholder="Add kindling CI workflow"
                    value={commitMessage}
                    onChange={(e) => setCommitMessage(e.target.value)}
                  />
                </div>
              </div>
              <div className="analyze-push-file-preview">
                <span className="analyze-push-file-icon">📄</span>
                <code>{generateResult.path}</code>
              </div>
              <div className="form-actions">
                <ActionButton
                  icon="🚀"
                  label="Commit & Push"
                  onClick={handlePush}
                  primary
                />
              </div>
            </div>
          )}

          {/* Push progress */}
          {(pushMessages.length > 0 || pushResult) && (
            <div className="analyze-generate-output">
              {pushMessages.length > 0 && (
                <div className="log-viewer" style={{ marginBottom: 12 }}>
                  <pre className="log-output">
                    {pushMessages.map((m, i) => <div key={i}>{m}</div>)}
                  </pre>
                </div>
              )}
              {pushRunning && pushMessages.length === 0 && (
                <p style={{ color: 'var(--text-tertiary)' }}>Preparing commit…</p>
              )}
              {pushResult && (
                <div className={`analyze-generate-result ${pushResult.ok ? 'success' : 'error'}`}>
                  <span>{pushResult.ok ? '✅' : '❌'}</span>
                  <span>{pushResult.output || pushResult.error}</span>
                </div>
              )}
              {pushResult?.ok && (
                <p style={{ fontSize: 12, color: 'var(--text-tertiary)', marginTop: 8 }}>
                  Your CI runner should pick up the build shortly. Check the <strong>Runners</strong> page to monitor progress.
                </p>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

/* ── Category card ─────────────────────────────────────────── */

const CATEGORY_ICONS: Record<string, string> = {
  Git: '🔀',
  'CI Workflow': '⚙️',
  Dockerfiles: '🐳',
  Dependencies: '📦',
  'Project Structure': '🏗️',
  Architecture: '🤖',
  Secrets: '🔑',
  Cluster: '☸️',
};

function AnalyzeCategoryCard({ category }: { category: AnalyzeCategory }) {
  const icon = CATEGORY_ICONS[category.category] || '📋';
  const passCount = category.checks.filter(c => c.status === 'pass').length;
  const failCount = category.checks.filter(c => c.status === 'fail').length;
  const warnCount = category.checks.filter(c => c.status === 'warn').length;
  const allPass = failCount === 0 && warnCount === 0 && passCount > 0;

  return (
    <div className={`analyze-cat-card ${allPass ? 'all-pass' : failCount > 0 ? 'has-fail' : ''}`}>
      <div className="analyze-cat-header">
        <span className="analyze-cat-icon">{icon}</span>
        <span className="analyze-cat-name">{category.category}</span>
        {allPass && <span className="analyze-cat-badge pass">✓</span>}
        {failCount > 0 && <span className="analyze-cat-badge fail">{failCount}</span>}
        {warnCount > 0 && failCount === 0 && <span className="analyze-cat-badge warn">{warnCount}</span>}
      </div>
      <div className="analyze-cat-checks">
        {category.checks.map((check, i) => (
          <div key={i} className={`analyze-check analyze-check-${check.status}`}>
            <span className="analyze-check-icon">
              {check.status === 'pass' ? '✅' : check.status === 'fail' ? '❌' : check.status === 'warn' ? '⚠️' : 'ℹ️'}
            </span>
            <div className="analyze-check-content">
              <span className="analyze-check-msg">{check.message}</span>
              {check.fix && (
                <code className="analyze-check-fix">{check.fix}</code>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
