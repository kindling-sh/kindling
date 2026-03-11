import { useState, useRef, useEffect, useCallback } from 'react';

interface PathAutocompleteProps {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  style?: React.CSSProperties;
  className?: string;
}

export function PathAutocomplete({
  value,
  onChange,
  placeholder,
  style,
  className = 'form-input',
}: PathAutocompleteProps) {
  const [suggestions, setSuggestions] = useState<string[]>([]);
  const [open, setOpen] = useState(false);
  const [activeIdx, setActiveIdx] = useState(-1);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout>>(undefined);

  // Fetch completions with debounce
  const fetchSuggestions = useCallback((prefix: string) => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(async () => {
      if (!prefix || prefix.length < 1) {
        setSuggestions([]);
        setOpen(false);
        return;
      }
      try {
        const res = await fetch(`/api/fs/complete?prefix=${encodeURIComponent(prefix)}`);
        if (!res.ok) { setSuggestions([]); return; }
        const data = await res.json();
        const entries: string[] = data.entries || [];
        setSuggestions(entries);
        setOpen(entries.length > 0);
        setActiveIdx(-1);
      } catch {
        setSuggestions([]);
        setOpen(false);
      }
    }, 150);
  }, []);

  // Trigger fetch on value change
  useEffect(() => {
    fetchSuggestions(value);
    return () => { if (debounceRef.current) clearTimeout(debounceRef.current); };
  }, [value, fetchSuggestions]);

  // Close on outside click
  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (wrapperRef.current && !wrapperRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, []);

  function handleKeyDown(e: React.KeyboardEvent) {
    if (!open || suggestions.length === 0) {
      // Open suggestions on slash key or typing
      if (e.key === '/' || e.key === '~') return; // let normal input handle
      return;
    }
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        setActiveIdx((i) => Math.min(i + 1, suggestions.length - 1));
        break;
      case 'ArrowUp':
        e.preventDefault();
        setActiveIdx((i) => Math.max(i - 1, 0));
        break;
      case 'Enter':
      case 'Tab':
        if (activeIdx >= 0 && activeIdx < suggestions.length) {
          e.preventDefault();
          selectSuggestion(suggestions[activeIdx]);
        } else if (suggestions.length === 1) {
          e.preventDefault();
          selectSuggestion(suggestions[0]);
        }
        break;
      case 'Escape':
        e.preventDefault();
        setOpen(false);
        break;
    }
  }

  function selectSuggestion(path: string) {
    onChange(path);
    setOpen(false);
    setActiveIdx(-1);
    // Keep focus so user can keep typing deeper paths
    inputRef.current?.focus();
  }

  // Shorten display paths for readability
  function displayPath(fullPath: string) {
    const parts = fullPath.replace(/\/$/, '').split('/');
    if (parts.length > 3) {
      return '…/' + parts.slice(-2).join('/') + '/';
    }
    return fullPath;
  }

  return (
    <div className="path-autocomplete" ref={wrapperRef} style={style}>
      <input
        ref={inputRef}
        className={className}
        placeholder={placeholder}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={handleKeyDown}
        onFocus={() => { if (value && suggestions.length > 0) setOpen(true); }}
        autoComplete="off"
        spellCheck={false}
      />
      {open && suggestions.length > 0 && (
        <div className="path-autocomplete-dropdown">
          {suggestions.map((s, i) => (
            <div
              key={s}
              className={`path-autocomplete-item ${i === activeIdx ? 'active' : ''}`}
              onMouseEnter={() => setActiveIdx(i)}
              onMouseDown={(e) => { e.preventDefault(); selectSuggestion(s); }}
            >
              <span className="path-autocomplete-icon">▷</span>
              <span className="path-autocomplete-full">{displayPath(s)}</span>
              {s !== displayPath(s) && (
                <span className="path-autocomplete-hint" title={s}>{s}</span>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
