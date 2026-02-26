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
            Kindling sets up your CI pipeline in minutes — real builds, real
            deploys, running locally for free. Then it stays with you: live file
            sync, 15 auto-provisioned dependencies, secrets management, public
            HTTPS tunnels, and a visual dashboard. Start fast, keep building.
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
                {"  "}✅ Found 3 Dockerfiles, 4 manifests{"\n"}
                ▸ Generating workflow with AI{"\n"}
                {"  "}🤖 Provider: openai, Model: o3{"\n"}
                {"  "}✅ Workflow written to dev-deploy.yml
              </span>
              {"\n\n"}
              <span className={styles.termPrompt}>$</span> git push{"\n"}
              <span className={styles.termDim}>
                {"  "}🏗️ Building → registry:5000/app:abc123{"\n"}
                {"  "}✅ Built & pushed{"\n"}
                {"  "}📦 Deploying with postgres + redis{"\n"}
                {"  "}✅ http://you-app.localhost
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
    emoji: "⚡",
    title: "Zero Cloud CI Minutes",
    description:
      "Your laptop is the runner. No queuing behind other jobs, no paying for compute you already own. Builds happen locally in seconds.",
  },
  {
    emoji: "🤖",
    title: "AI-Generated Workflows",
    description:
      "Point kindling generate at any repo. It scans Dockerfiles, docker-compose, Helm charts, and source code, then produces a complete GitHub Actions workflow.",
  },
  {
    emoji: "📦",
    title: "15 Auto-Provisioned Dependencies",
    description:
      "Declare postgres, redis, rabbitmq, kafka, elasticsearch, and 10 more in your workflow. The operator provisions them and injects connection URLs automatically.",
  },
  {
    emoji: "🔨",
    title: "Kaniko Builds — No Docker Daemon",
    description:
      "Images are built inside the cluster using Kaniko. No Docker-in-Docker, no privileged containers. Layer caching makes rebuilds fast.",
  },
  {
    emoji: "🌐",
    title: "Instant localhost Staging",
    description:
      "Every push deploys a full staging environment with Deployment, Service, and Ingress — accessible at http://you-app.localhost immediately.",
  },
  {
    emoji: "🔐",
    title: "Secrets & OAuth Built In",
    description:
      "Manage API keys with kindling secrets. Need OAuth callbacks? kindling expose creates a public HTTPS tunnel with one command.",
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
          Start with CI. Stay for everything else.
        </h2>
        <p className={styles.sectionSubtitle}>
          One CLI. Pipeline in minutes. Dev environment that grows with you.
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
              <code>kindling init</code> creates a Kind cluster with an
              in-cluster registry, ingress controller, and the kindling
              operator.
            </p>
          </div>
          <div className={styles.stepArrow}>→</div>
          <div className={styles.step}>
            <div className={styles.stepNumber}>2</div>
            <h3>Connect</h3>
            <p>
              <code>kindling runners</code> registers a self-hosted GitHub
              Actions runner bound to your repo and username.
            </p>
          </div>
          <div className={styles.stepArrow}>→</div>
          <div className={styles.step}>
            <div className={styles.stepNumber}>3</div>
            <h3>Generate</h3>
            <p>
              <code>kindling generate</code> scans your repo and uses AI to
              produce a complete workflow with builds, deploys, and dependencies.
            </p>
          </div>
          <div className={styles.stepArrow}>→</div>
          <div className={styles.step}>
            <div className={styles.stepNumber}>4</div>
            <h3>Push & Deploy</h3>
            <p>
              <code>git push</code> triggers the workflow. Your laptop builds
              the image and deploys a full staging environment on localhost.
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
