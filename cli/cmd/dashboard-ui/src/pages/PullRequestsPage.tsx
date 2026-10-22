import { useState } from 'react';
import { useApi, fetchGitHubPRConfig, saveGitHubPRConfig, fetchStagingPRDiff, streamStagingPRPromote } from '../api';
import type { PRInfo, PRDiffResult } from '../api';
import { EmptyState, TimeAgo } from './shared';
import { ActionButton, ActionModal, ConfirmDialog, useToast } from './actions';

export function PullRequestsPage() {
  const { data, loading, refresh } = useApi<{ prs: PRInfo[] }>('/api/staging/prs', 15000);
  const { toast } = useToast();

  const [showSettings, setShowSettings] = useState(false);
  const [savingSettings, setSavingSettings] = useState(false);
  const [settingsForm, setSettingsForm] = useState({
    token: '', owner: '', repo: '', gitops_repo: '', gitops_path: '', gitops_base_branch: '', workflow: '',
  });

  const [diffTarget, setDiffTarget] = useState<PRInfo | null>(null);
  const [diffLoading, setDiffLoading] = useState(false);
  const [diffResult, setDiffResult] = useState<PRDiffResult | null>(null);

  const [promoteTarget, setPromoteTarget] = useState<PRInfo | null>(null);
  const [promoting, setPromoting] = useState(false);
  const [promoteLogs, setPromoteLogs] = useState<{ type: string; message: string }[]>([]);

  async function openSettings() {
    const cfg = await fetchGitHubPRConfig().catch(() => null);
    if (cfg) {
      setSettingsForm({
        token: '',
        owner: cfg.owner_override,
        repo: cfg.repo_override,
        gitops_repo: cfg.gitops_repo,
        gitops_path: cfg.gitops_path,
        gitops_base_branch: cfg.gitops_base_branch,
        workflow: cfg.workflow,
      });
    }
    setShowSettings(true);
  }

  async function handleSaveSettings() {
    setSavingSettings(true);
    const result = await saveGitHubPRConfig(settingsForm);
    setSavingSettings(false);
    if (result.ok) {
      toast('GitHub settings saved', 'success');
      setShowSettings(false);
      refresh();
    } else {
      toast(result.error || 'Failed to save settings', 'error');
    }
  }

  async function openDiff(pr: PRInfo) {
    setDiffTarget(pr);
    setDiffResult(null);
    setDiffLoading(true);
    try {
      const result = await fetchStagingPRDiff(pr.number);
      setDiffResult(result);
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Failed to load diff', 'error');
      setDiffTarget(null);
    }
    setDiffLoading(false);
  }

  function startPromote() {
    if (!promoteTarget) return;
    setPromoting(true);
    setPromoteLogs([]);
    streamStagingPRPromote(promoteTarget.number, (msg) => {
      setPromoteLogs((prev) => [...prev, msg]);
      if (msg.type === 'done') {
        toast(msg.message, 'success');
        setPromoting(false);
        refresh();
      } else if (msg.type === 'error') {
        toast(msg.message, 'error');
        setPromoting(false);
      }
    });
  }

  if (loading) return <div className="loading">Loading pull requests…</div>;

  const prs = data?.prs || [];

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header-left">
          <h1>Pull Requests</h1>
          <p className="page-subtitle">Staging previews from kindling-pr-preview, and manual promote via kindling-gitops-handoff</p>
        </div>
        <div className="page-actions">
          <ActionButton icon="⚙" label="GitHub Settings" onClick={openSettings} />
        </div>
      </div>

      {prs.length === 0 ? (
        <EmptyState
          icon="⇄"
          message="No open pull requests found (or none have a kindling-pr-preview artifact yet) — check your GitHub settings if this looks wrong"
        />
      ) : (
        <table className="data-table">
          <thead>
            <tr>
              <th>PR</th>
              <th>Branch</th>
              <th>Preview</th>
              <th>Last deployed</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {prs.map((pr) => (
              <tr key={pr.number}>
                <td>
                  <a href={pr.url} target="_blank" rel="noopener noreferrer">#{pr.number} {pr.title}</a>
                </td>
                <td className="mono">{pr.branch}</td>
                <td>
                  {pr.has_preview ? (
                    <span className="badge badge-ok">deployed</span>
                  ) : (
                    <span className="badge badge-warn">no preview yet</span>
                  )}
                </td>
                <td><TimeAgo timestamp={pr.artifact_created_at} /></td>
                <td style={{ display: 'flex', gap: 8 }}>
                  <ActionButton small label="View diff" onClick={() => openDiff(pr)} disabled={!pr.has_preview} />
                  <ActionButton small primary label="Promote now" onClick={() => setPromoteTarget(pr)} disabled={!pr.has_preview} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {showSettings && (
        <ActionModal
          title="GitHub Settings"
          submitLabel="Save"
          loading={savingSettings}
          onSubmit={handleSaveSettings}
          onClose={() => setShowSettings(false)}
        >
          <label className="form-label">GitHub Token</label>
          <input className="form-input" type="password" placeholder="leave blank to keep the current one"
            value={settingsForm.token} onChange={(e) => setSettingsForm({ ...settingsForm, token: e.target.value })} />
          <span className="form-hint">Needs `pull_requests:read` + `actions:read,write` (or classic `repo` scope)</span>

          <label className="form-label">Owner <span className="text-dim">(optional — auto-detected from git remote)</span></label>
          <input className="form-input" placeholder="e.g. myorg" value={settingsForm.owner}
            onChange={(e) => setSettingsForm({ ...settingsForm, owner: e.target.value })} />
          <label className="form-label">Repo <span className="text-dim">(optional)</span></label>
          <input className="form-input" placeholder="e.g. my-app" value={settingsForm.repo}
            onChange={(e) => setSettingsForm({ ...settingsForm, repo: e.target.value })} />

          <label className="form-label">GitOps Repo <span className="text-dim">(optional — defaults to the current repo)</span></label>
          <input className="form-input" placeholder="owner/gitops-repo" value={settingsForm.gitops_repo}
            onChange={(e) => setSettingsForm({ ...settingsForm, gitops_repo: e.target.value })} />
          <label className="form-label">GitOps Path <span className="text-dim">(default: environments/production)</span></label>
          <input className="form-input" placeholder="environments/production" value={settingsForm.gitops_path}
            onChange={(e) => setSettingsForm({ ...settingsForm, gitops_path: e.target.value })} />
          <label className="form-label">GitOps Base Branch <span className="text-dim">(default: main)</span></label>
          <input className="form-input" placeholder="main" value={settingsForm.gitops_base_branch}
            onChange={(e) => setSettingsForm({ ...settingsForm, gitops_base_branch: e.target.value })} />
          <label className="form-label">Promote Workflow File <span className="text-dim">(default: promote-pr.yml)</span></label>
          <input className="form-input" placeholder="promote-pr.yml" value={settingsForm.workflow}
            onChange={(e) => setSettingsForm({ ...settingsForm, workflow: e.target.value })} />
        </ActionModal>
      )}

      {diffTarget && (
        <ActionModal
          title={`Diff for PR #${diffTarget.number}`}
          onClose={() => { setDiffTarget(null); setDiffResult(null); }}
        >
          {diffLoading ? (
            <div className="loading">Loading diff…</div>
          ) : diffResult ? (
            <pre className="diff-view">
              {diffResult.diff_lines.map((l, i) => (
                <div key={i} className={`diff-line diff-${l.type}`}>
                  {l.type === 'add' ? '+ ' : l.type === 'remove' ? '- ' : '  '}{l.text}
                </div>
              ))}
              {diffResult.diff_lines.length === 0 && <div className="text-dim">No changes</div>}
            </pre>
          ) : (
            <div className="text-dim">No diff available</div>
          )}
        </ActionModal>
      )}

      {promoteTarget && !promoting && (
        <ConfirmDialog
          title="Promote now"
          message={`Promote PR #${promoteTarget.number} ("${promoteTarget.title}") into the configured GitOps repo? This commits (or opens a PR for) the exact previously-reviewed values-prod.yaml — the same thing a real merge of this PR would do automatically.`}
          confirmLabel="Promote"
          onConfirm={startPromote}
          onCancel={() => setPromoteTarget(null)}
        />
      )}

      {promoteTarget && promoting && (
        <ActionModal
          title={`Promoting PR #${promoteTarget.number}`}
          loading={promoting}
          onClose={() => { if (!promoting) { setPromoteTarget(null); setPromoteLogs([]); } }}
        >
          <div className="log-output">
            {promoteLogs.map((l, i) => (
              <div key={i} className={`log-line log-${l.type}`}>{l.message}</div>
            ))}
          </div>
        </ActionModal>
      )}
    </div>
  );
}
