import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebars: SidebarsConfig = {
  docsSidebar: [
    {
      type: "doc",
      id: "quickstart",
      label: "Quickstart",
    },
    {
      type: "category",
      label: "Getting Started",
      collapsed: false,
      items: ["analyze", "intel", "generate", "runners"],
    },
    {
      type: "category",
      label: "Deploy & Configure",
      collapsed: false,
      items: ["crd-reference", "dependencies", "secrets", "env-vars"],
    },
    {
      type: "category",
      label: "Inner Dev Loop",
      collapsed: false,
      items: ["load", "sync", "debugging", "dev-mode", "oauth-tunnels"],
    },
    {
      type: "category",
      label: "Production",
      collapsed: false,
      items: ["graduation", "tls"],
    },
    {
      type: "category",
      label: "CI Workflows",
      collapsed: false,
      items: ["github-actions", "gitlab-ci"],
    },
    {
      type: "category",
      label: "Walkthroughs",
      collapsed: true,
      items: [
        "guides/stripe-integration",
        "guides/auth0-integration",
        "guides/multi-service",
        "guides/background-workers",
        "guides/websocket-realtime",
        "guides/webhook-testing",
        "guides/s3-file-uploads",
      ],
    },
    {
      type: "category",
      label: "Agent Development",
      collapsed: true,
      items: [
        "guides/rag-langchain",
        "guides/crewai-multi-agent",
        "guides/langgraph-stateful",
        "guides/openai-agents-sdk",
        "guides/mongodb-atlas-vectors",
        "guides/neondb-seeded-data",
      ],
    },
    {
      type: "category",
      label: "Reference",
      collapsed: true,
      items: [
        "cli",
        "dashboard",
        "architecture",
        "guides/manual-deploy",
        "guides/manual-workflow",
        "guides/docker-resources",
      ],
    },
  ],
};

export default sidebars;
