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
      label: "Onboarding",
      collapsed: false,
      items: ["analyze", "intel", "generate", "secrets", "dependencies"],
    },
    {
      type: "category",
      label: "Dev Loop",
      collapsed: false,
      items: [
        "sync",
        "debugging",
        "dev-mode",
        "dashboard",
        "env-vars",
        "github-actions",
        "gitlab-ci",
        "oauth-tunnels",
        "graduation",
      ],
    },
    {
      type: "category",
      label: "Walkthroughs",
      collapsed: false,
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
      collapsed: false,
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
      collapsed: false,
      items: [
        "cli",
        "crd-reference",
        "architecture",
        "guides/manual-deploy",
        "guides/manual-workflow",
        "guides/docker-resources",
      ],
    },
  ],
};

export default sidebars;
