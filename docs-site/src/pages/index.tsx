import { useState } from "react";
import clsx from "clsx";
import Link from "@docusaurus/Link";
import useDocusaurusContext from "@docusaurus/useDocusaurusContext";
import Layout from "@theme/Layout";
import styles from "./index.module.css";

const INSTALL_CMD = "brew install kindling-sh/tap/kindling";

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
          <p className={styles.heroSubtitle}>{siteConfig.tagline}</p>
          <p className={styles.heroDescription}>
            Multi-agent systems are the next wave — orchestrators, tool-calling
            agents, RAG services, vector stores, queues, and APIs, all talking
            to each other. Kindling gives you the infrastructure to build them
            from day one: a local Kubernetes cluster, a CI pipeline generated in
            minutes, and every service deployed with its dependencies in one
            push. Then it stays with you — live sync across agents, a visual
            dashboard for your entire system, secrets, and public tunnels for
            webhooks. Start with CI. Keep building.
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
              GitHub
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
                {"  "}✅ Kind cluster created{"\n"}
                ▸ Installing ingress + registry{"\n"}
                {"  "}✅ Ingress and registry ready{"\n"}
                ▸ Deploying operator{"\n"}
                {"  "}✅ Controller is running{"\n"}
                {"\n"}
                {"  "}🎉 kindling is ready!
              </span>
              {"\n\n"}
              <span className={styles.termPrompt}>$</span> kindling generate -k
              sk-... -r .{"\n"}
              <span className={styles.termDim}>
                ▸ Analyzing repository{"\n"}
                {"  "}✅ Found orchestrator, 3 agents, RAG service{"\n"}
                ▸ Generating workflow with AI{"\n"}
                {"  "}🤖 Provider: openai, Model: o3{"\n"}
                {"  "}✅ Workflow written to dev-deploy.yml
              </span>
              {"\n\n"}
              <span className={styles.termPrompt}>$</span> git push{"\n"}
              <span className={styles.termDim}>
                {"  "}🏗️ Building 5 services...{"\n"}
                {"  "}✅ All images built & pushed{"\n"}
                {"  "}📦 Deploying with postgres + redis + qdrant{"\n"}
                {"  "}✅ http://agent-orchestrator.localhost
              </span>
            </code>
          </div>
        </div>
      </div>
    </header>
  );
}

type FeatureItem = {
  emoji: string;
  title: string;
  description: string;
};

const features: FeatureItem[] = [
  {
    emoji: "�",
    title: "Built for Agent Architectures",
    description:
      "Orchestrators, tool-calling agents, RAG pipelines, memory services — deploy your entire agent system with Postgres, Redis, message queues, and vector stores, all wired together locally.",
  },
  {
    emoji: "⚡",
    title: "CI Pipeline in Minutes",
    description:
      "Kindling scans your repo and generates a complete CI workflow. Every agent and service builds and deploys on your laptop — no cloud runners, no queuing, no billing.",
  },
  {
    emoji: "🔄",
    title: "Live Sync Across Agents",
    description:
      "Iterating on a prompt, a tool, or agent logic? Edit the code and see it running instantly. Kindling detects 30+ runtimes and picks the right restart strategy for each service.",
  },
  {
    emoji: "🧩",
    title: "15 Infrastructure Dependencies",
    description:
      "Declare postgres, redis, kafka, elasticsearch, and 11 more in your workflow. The operator provisions them and injects connection URLs — the plumbing agents need to talk to each other.",
  },
  {
    emoji: "🖥️",
    title: "Visual Dashboard",
    description:
      "See every agent, service, and dependency in one UI. Tail logs from your orchestrator while syncing code to a tool agent. One-click rebuilds, scaling, and environment management.",
  },
  {
    emoji: "🔐",
    title: "Secrets & Webhooks",
    description:
      "Manage LLM API keys, database credentials, and third-party tokens across agents. Need webhook callbacks? One command creates a public HTTPS tunnel.",
  },
];

function FeatureCard({ emoji, title, description }: FeatureItem) {
  return (
    <div className={styles.featureCard}>
      <div className={styles.featureEmoji}>{emoji}</div>
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
          Everything your agent system needs
        </h2>
        <p className={styles.sectionSubtitle}>
          CI gets you running. The engine keeps you building.
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
            <h3>Bootstrap</h3>
            <p>
              <code>kindling init</code> spins up a Kubernetes cluster with a
              container registry, ingress, and operator — ready for your
              agent system.
            </p>
          </div>
          <div className={styles.stepArrow}>→</div>
          <div className={styles.step}>
            <div className={styles.stepNumber}>2</div>
            <h3>Generate & Push</h3>
            <p>
              <code>kindling generate</code> scans your repo — agents,
              services, infrastructure — and writes a CI workflow. Push
              once and everything deploys.
            </p>
          </div>
          <div className={styles.stepArrow}>→</div>
          <div className={styles.step}>
            <div className={styles.stepNumber}>3</div>
            <h3>Iterate</h3>
            <p>
              <code>kindling sync</code> live-syncs any agent or service.
              Tweak a prompt, update tool logic, change orchestration —
              see it running instantly.
            </p>
          </div>
          <div className={styles.stepArrow}>→</div>
          <div className={styles.step}>
            <div className={styles.stepNumber}>4</div>
            <h3>Keep Building</h3>
            <p>
              Add agents, swap models, wire up new tools. Manage secrets
              for every LLM provider. The engine grows with your system.
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
# — or build from source —
git clone https://github.com/kindling-sh/kindling.git
cd kindling && make cli
sudo mv bin/kindling /usr/local/bin/

# Bootstrap a local cluster
kindling init

# Register a GitHub Actions runner
kindling runners -u <github-user> \\
  -r <owner/repo> -t <pat>

# AI-generate a workflow for your app
kindling generate -k <api-key> -r /path/to/app

# Push and watch it deploy
git push origin main

# Access your app
curl http://<user>-<app>.localhost`}
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
