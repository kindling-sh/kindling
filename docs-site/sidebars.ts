import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebars: SidebarsConfig = {
  docsSidebar: [
    {
      type: "doc",
      id: "getting-started",
      label: "Getting Started",
    },
    {
      type: "doc",
      id: "cli",
      label: "CLI Reference",
    },
    {
      type: "doc",
      id: "architecture",
      label: "Architecture",
    },
    {
      type: "doc",
      id: "github-actions",
      label: "GitHub Actions",
    },
    {
      type: "doc",
      id: "dependencies",
      label: "Dependencies",
    },
    {
      type: "doc",
      id: "crd-reference",
      label: "CRD Reference",
    },
    {
      type: "doc",
      id: "secrets",
      label: "Secrets Management",
    },
    {
      type: "doc",
      id: "oauth-tunnels",
      label: "OAuth & Tunnels",
    },
  ],
};

export default sidebars;
