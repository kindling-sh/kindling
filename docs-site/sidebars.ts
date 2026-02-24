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
      label: "Features",
      collapsed: false,
      items: [
        "secrets",
        "dependencies",
        "github-actions",
        "generate",
        "oauth-tunnels",
        "sync",
        "env-vars",
        "dashboard",
      ],
    },
    {
      type: "category",
      label: "Guides",
      collapsed: false,
      items: [
        "guides/stripe-integration",
        "guides/auth0-integration",
        "guides/multi-service",
        "guides/manual-deploy",
        "guides/manual-workflow",
        "guides/docker-resources",
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
      ],
    },
  ],
};

export default sidebars;
