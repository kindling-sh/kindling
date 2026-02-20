import { useState, useCallback, createContext, useContext } from 'react';
import type { ReactNode } from 'react';
import type { ActionResult } from '../api';

// ── Toast notifications ──────────────────────────────────────────

interface Toast {
  id: number;
  message: string;
  type: 'success' | 'error';
}

interface ToastContextValue {
  toast: (message: string, type: 'success' | 'error') => void;
}

const ToastContext = createContext<ToastContextValue>({ toast: () => {} });

export function useToast() {
  return useContext(ToastContext);
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  let nextId = 0;

  const toast = useCallback((message: string, type: 'success' | 'error') => {
    const id = ++nextId;
    setToasts((t) => [...t, { id, message, type }]);
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 4000);
  }, []);

  return (
    <ToastContext.Provider value={{ toast }}>
      {children}
      <div className="toast-container">
        {toasts.map((t) => (
          <div key={t.id} className={`toast toast-${t.type}`}>
            <span>{t.type === 'success' ? '✅' : '❌'}</span>
            <span>{t.message}</span>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

// ── Confirmation dialog ──────────────────────────────────────────

export function ConfirmDialog({
  title,
  message,
  confirmLabel = 'Confirm',
  danger = false,
  onConfirm,
  onCancel,
}: {
  title: string;
  message: string;
  confirmLabel?: string;
  danger?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="modal-overlay" onClick={onCancel}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <h3>{title}</h3>
        </div>
        <div className="modal-body">
          <p>{message}</p>
        </div>
        <div className="modal-footer">
          <button className="btn btn-secondary" onClick={onCancel}>Cancel</button>
          <button
            className={`btn ${danger ? 'btn-danger' : 'btn-primary'}`}
            onClick={onConfirm}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}

// ── Action modal (form-based) ────────────────────────────────────

export function ActionModal({
  title,
  children,
  submitLabel = 'Submit',
  loading = false,
  onSubmit,
  onClose,
}: {
  title: string;
  children: ReactNode;
  submitLabel?: string;
  loading?: boolean;
  onSubmit: () => void;
  onClose: () => void;
}) {
  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal modal-wide" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <h3>{title}</h3>
          <button className="btn btn-sm" onClick={onClose}>✕</button>
        </div>
        <div className="modal-body">
          {children}
        </div>
        <div className="modal-footer">
          <button className="btn btn-secondary" onClick={onClose} disabled={loading}>Cancel</button>
          <button className="btn btn-primary" onClick={onSubmit} disabled={loading}>
            {loading ? 'Working…' : submitLabel}
          </button>
        </div>
      </div>
    </div>
  );
}

// ── Inline action button ─────────────────────────────────────────

export function ActionButton({
  label,
  icon,
  onClick,
  danger = false,
  small = false,
  disabled = false,
}: {
  label: string;
  icon?: string;
  onClick: () => void;
  danger?: boolean;
  small?: boolean;
  disabled?: boolean;
}) {
  return (
    <button
      className={`btn ${danger ? 'btn-danger' : 'btn-primary'} ${small ? 'btn-sm' : ''}`}
      onClick={onClick}
      disabled={disabled}
    >
      {icon && <span>{icon} </span>}
      {label}
    </button>
  );
}

// ── Result output block ──────────────────────────────────────────

export function ResultOutput({ result }: { result: ActionResult | null }) {
  if (!result) return null;
  return (
    <div className={`result-output ${result.ok ? 'result-success' : 'result-error'}`}>
      <pre>{result.ok ? result.output : result.error}</pre>
    </div>
  );
}
