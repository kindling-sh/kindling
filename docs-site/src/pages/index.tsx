import { useState } from "react";
import clsx from "clsx";
import Link from "@docusaurus/Link";
import useDocusaurusContext from "@docusaurus/useDocusaurusContext";
import Layout from "@theme/Layout";
import styles from "./index.module.css";

const INSTALL_CMD = "brew install kindling-sh/tap/kindling";

// ── SVG Icons (stroke-based, dashboard-aligned) ─────────────────

function IconSearch({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="11" cy="11" r="8" />
      <line x1="21" y1="21" x2="16.65" y2="16.65" />
    </svg>
  );
}

function IconCpu({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="4" y="4" width="16" height="16" rx="2" />
      <rect x="9" y="9" width="6" height="6" />
      <line x1="9" y1="1" x2="9" y2="4" /><line x1="15" y1="1" x2="15" y2="4" />
      <line x1="9" y1="20" x2="9" y2="23" /><line x1="15" y1="20" x2="15" y2="23" />
      <line x1="20" y1="9" x2="23" y2="9" /><line x1="20" y1="14" x2="23" y2="14" />
      <line x1="1" y1="9" x2="4" y2="9" /><line x1="1" y1="14" x2="4" y2="14" />
    </svg>
  );
}

function IconZap({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />
    </svg>
  );
}

function IconPackage({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <line x1="16.5" y1="9.4" x2="7.5" y2="4.21" />
      <path d="M21 16V8a2 2 0 00-1-1.73l-7-4a2 2 0 00-2 0l-7 4A2 2 0 003 8v8a2 2 0 001 1.73l7 4a2 2 0 002 0l7-4A2 2 0 0021 16z" />
      <polyline points="3.27 6.96 12 12.01 20.73 6.96" />
      <line x1="12" y1="22.08" x2="12" y2="12" />
    </svg>
  );
}

function IconBrain({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M9.5 2A5.5 5.5 0 005 7.5c0 1.02.28 1.97.76 2.79" />
      <path d="M14.5 2A5.5 5.5 0 0120 7.5c0 1.02-.28 1.97-.76 2.79" />
      <path d="M4.76 10.29A5.5 5.5 0 003 14.5 5.5 5.5 0 008.5 20h1" />
      <path d="M19.24 10.29A5.5 5.5 0 0121 14.5a5.5 5.5 0 01-5.5 5.5h-1" />
      <line x1="12" y1="2" x2="12" y2="22" />
    </svg>
  );
}

function IconGlobe({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="10" />
      <line x1="2" y1="12" x2="22" y2="12" />
      <path d="M12 2a15.3 15.3 0 014 10 15.3 15.3 0 01-4 10 15.3 15.3 0 01-4-10 15.3 15.3 0 014-10z" />
    </svg>
  );
}

function IconShield({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
    </svg>
  );
}

function IconStar({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" stroke="none">
      <polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2" />
    </svg>
  );
}

function InstallCommand() {
  const [copied, setCopied] = useState(false);

  const handleCopy = () => {
    navigator.clipboard.writeText(INSTALL_CMD);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className={styles.installBanner} onClick={handleCopy} role="button" tabIndex={0}>
      <span className={styles.installPrompt}>$</span>
      <span className={styles.installText}>{INSTALL_CMD}</span>
      <span className={styles.installCopy}>{copied ? "✓ Copied" : "Copy"}</span>
    </div>
  );
}

function HeroSection() {
  const { siteConfig } = useDocusaurusContext();
  return (
    <header className={styles.hero}>
      <div className={styles.heroInner}>
        <div className={styles.heroGlow} />
        <div className={styles.heroContent}>
          <div className={styles.heroEmber}>🔥</div>
          <h1 className={styles.heroTitle}>
            <span className={styles.heroTitleGradient}>kindling</span>
          </h1>
          <div className={styles.platformBanner}>
            <span className={styles.platformBannerItem}>
              <svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor">
                <path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0024 12c0-6.63-5.37-12-12-12z" />
              </svg>
              GitHub Actions
            </span>
            <span className={styles.platformBannerItem} data-platform="gitlab">
              <svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor">
                <path d="M23.955 13.587l-1.342-4.135-2.664-8.189a.455.455 0 00-.867 0L16.418 9.45H7.582L4.918 1.263a.455.455 0 00-.867 0L1.386 9.452.044 13.587a.924.924 0 00.331 1.023L12 23.054l11.625-8.443a.92.92 0 00.33-1.024" />
              </svg>
              GitLab CI
            </span>
          </div>
          <p className={styles.heroSubtitle}>{siteConfig.tagline}</p>
          <p className={styles.heroDescription}>
            Minimize the time from idea to prod. Designed for builders, Kindling
            enforces SDLC best practices so your project — multi-agent
            architectures, microservices or any flavor of containerized apps —
            runs everywhere. Your project just works. In prod.
          </p>
          <InstallCommand />
          <div className={styles.heroButtons}>
            <Link
              className={clsx("button button--lg", styles.heroPrimary)}
              to="/docs/quickstart"
            >
              Get Started →
            </Link>
            <Link
              className={clsx("button button--lg", styles.heroSecondary)}
              to="https://github.com/kindling-sh/kindling"
            >
              <IconStar size={16} /> Star on GitHub
            </Link>
          </div>
        </div>

        <div className={styles.heroTerminal}>
          <div className={styles.terminalBar}>
            <span className={styles.terminalDot} data-color="red" />
            <span className={styles.terminalDot} data-color="yellow" />
            <span className={styles.terminalDot} data-color="green" />
            <span className={styles.terminalTitle}>terminal</span>
          </div>
          <div className={styles.terminalBody}>
            <code>
              <span className={styles.termPrompt}>$</span> kindling init
              {"\n"}
              <span className={styles.termDim}>
                ▸ Creating Kind cluster{"\n"}
                {"  "}✅ Cluster, registry, operator ready
              </span>
              {"\n\n"}
              <span className={styles.termPrompt}>$</span> kindling analyze
              {"\n"}
              <span className={styles.termDim}>
                {"  "}✅ 2 Dockerfiles, 3 dependencies{"\n"}
                {"  "}ℹ️  Agent frameworks: LangChain{"\n"}
                {"  "}✅ Ready for 'kindling generate'
              </span>
              {"\n\n"}
              <span className={styles.termPrompt}>$</span> kindling generate -k
              sk-... -r .{"\n"}
              <span className={styles.termDim}>
                {"  "}🤖 Provider: openai, Model: o3{"\n"}
                {"  "}✅ Workflow written to dev-deploy.yml
              </span>
              {"\n\n"}
              <span className={styles.termPrompt}>$</span> git push{"\n"}
              <span className={styles.termDim}>
                {"  "}🏗️ Building → registry:5000/app:abc123{"\n"}
                {"  "}✅ http://you-app.localhost
              </span>
              {"\n\n"}
              <span className={styles.termPrompt}>$</span> kindling sync -d
              you-app --restart{"\n"}
              <span className={styles.termDim}>
                {"  "}🔄 Watching for changes...{"\n"}
                {"  "}✅ Synced 3 files → restarted
              </span>
            </code>
          </div>
        </div>
      </div>
    </header>
  );
}

type FeatureItem = {
  icon: React.ReactNode;
  title: string;
  description: string;
};

const features: FeatureItem[] = [
  {
    icon: <IconSearch />,
    title: "Analyze Before You Build",
    description:
      "kindling analyze checks your repo's readiness — Dockerfiles, dependencies, secrets, agent architecture, Kaniko compatibility — before you generate a single line of CI.",
  },
  {
    icon: <IconCpu />,
    title: "AI-Generated Workflows",
    description:
      "kindling generate scans your repo and produces a complete GitHub Actions or GitLab CI workflow. Detects agent frameworks, MCP servers, inter-service calls, and secrets.",
  },
  {
    icon: <IconZap />,
    title: "Two-Speed Dev Loop",
    description:
      "Outer loop: git push → build → deploy. Inner loop: edit → sync → reload in under a second. Both run on your laptop, zero cloud CI minutes.",
  },
  {
    icon: <IconPackage />,
    title: "15 Auto-Provisioned Dependencies",
    description:
      "Declare postgres, redis, kafka, elasticsearch, and 11 more. The operator provisions them and injects connection URLs automatically.",
  },
  {
    icon: <IconBrain />,
    title: "Agent Intel",
    description:
      "Auto-configures GitHub Copilot, Claude Code, Cursor, and Windsurf with full project context. Activates on any command, restores originals when you're done.",
  },
  {
    icon: <IconGlobe />,
    title: "Localhost to Production",
    description:
      "Dev on localhost with instant staging. Need OAuth callbacks? kindling expose creates a public HTTPS tunnel. Ready to ship? kindling snapshot graduates to any cluster.",
  },
  {
    icon: <IconShield />,
    title: "Secrets & Credentials Built In",
    description:
      "Manage API keys with kindling secrets. Automatic detection during analyze and generate. Local backup survives cluster rebuilds.",
  },
];

function FeatureCard({ icon, title, description }: FeatureItem) {
  return (
    <div className={styles.featureCard}>
      <div className={styles.featureIcon}>{icon}</div>
      <h3 className={styles.featureTitle}>{title}</h3>
      <p className={styles.featureDescription}>{description}</p>
    </div>
  );
}

function FeaturesSection() {
  return (
    <section className={styles.features}>
      <div className="container">
        <h2 className={styles.sectionTitle}>
          Everything you need for local dev staging
        </h2>
        <p className={styles.sectionSubtitle}>
          One operator. One CLI. From git push to running app on localhost.
        </p>
        <div className={styles.featureGrid}>
          {features.map((f, idx) => (
            <FeatureCard key={idx} {...f} />
          ))}
        </div>
      </div>
    </section>
  );
}

function HowItWorksSection() {
  return (
    <section className={styles.howItWorks}>
      <div className="container">
        <h2 className={styles.sectionTitle}>How it works</h2>
        <div className={styles.stepsGrid}>
          <div className={styles.step}>
            <div className={styles.stepNumber}>1</div>
            <h3>Analyze</h3>
            <p>
              <code>kindling analyze</code> checks your project — Dockerfiles,
              dependencies, secrets, agent architecture — and tells you
              exactly what's ready.
            </p>
          </div>
          <div className={styles.stepArrow}>→</div>
          <div className={styles.step}>
            <div className={styles.stepNumber}>2</div>
            <h3>Generate</h3>
            <p>
              <code>kindling generate</code> scans your repo and uses AI to
              produce a complete CI workflow with builds, deploys, and dependencies.
            </p>
          </div>
          <div className={styles.stepArrow}>→</div>
          <div className={styles.step}>
            <div className={styles.stepNumber}>3</div>
            <h3>Dev Loop</h3>
            <p>
              <code>git push</code> builds and deploys.
              <code>kindling sync</code> gives you sub-second live reload.
              Iterate until it's right.
            </p>
          </div>
          <div className={styles.stepArrow}>→</div>
          <div className={styles.step}>
            <div className={styles.stepNumber}>4</div>
            <h3>Snapshot & Deploy</h3>
            <p>
              <code>kindling snapshot --deploy</code> copies images to your
              registry, generates a Helm chart, and deploys to any production
              cluster.
            </p>
          </div>
        </div>
      </div>
    </section>
  );
}

function QuickStartSection() {
  return (
    <section className={styles.quickStart}>
      <div className="container">
        <h2 className={styles.sectionTitle}>Quick start</h2>
        <div className={styles.quickStartGrid}>
          <div className={styles.quickStartCode}>
            <pre>
              <code>
                {`# Install
brew install kindling-sh/tap/kindling

# Bootstrap a local cluster
kindling init

# Register a CI runner (GitHub or GitLab)
kindling runners -u <user> \\
  -r <owner/repo> -t <pat>

# Check your project's readiness
kindling analyze

# AI-generate a workflow for your app
kindling generate -k <api-key> -r /path/to/app

# Push and watch it deploy
git push origin main

# Start live sync
kindling sync -d <user>-<app> --restart`}
              </code>
            </pre>
          </div>
          <div className={styles.quickStartInfo}>
            <h3>Prerequisites</h3>
            <ul>
              <li>
                <strong>Docker Desktop</strong> — container runtime
              </li>
              <li>
                <strong>Kind</strong> — local Kubernetes clusters
              </li>
              <li>
                <strong>kubectl</strong> — Kubernetes CLI
              </li>
            </ul>
            <h3>Supported languages</h3>
            <div className={styles.langTags}>
              {[
                "Go",
                "TypeScript",
                "Python",
                "Java",
                "Rust",
                "Ruby",
                "PHP",
                "C#",
                "Elixir",
              ].map((lang) => (
                <span key={lang} className={styles.langTag}>
                  {lang}
                </span>
              ))}
            </div>
            <h3>Supported AI providers</h3>
            <div className={styles.langTags}>
              {["OpenAI (o3, gpt-4o)", "Anthropic (Claude)"].map((p) => (
                <span key={p} className={styles.langTag}>
                  {p}
                </span>
              ))}
            </div>

            <div style={{ marginTop: "2rem" }}>
              <Link
                className={clsx("button button--lg", styles.heroPrimary)}
                to="/docs/quickstart"
              >
                Read the full guide →
              </Link>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

export default function Home(): JSX.Element {
  const { siteConfig } = useDocusaurusContext();
  return (
    <Layout title="Home" description={siteConfig.tagline}>
      <HeroSection />
      <main>
        <FeaturesSection />
        <HowItWorksSection />
        <QuickStartSection />
      </main>
    </Layout>
  );
}
