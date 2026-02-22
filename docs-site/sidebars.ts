import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebars: SidebarsConfig = {
  docsSidebar: [
    {
      type: "doc",
      id: "quickstart",
      label: "Quickstart",
    },
    {
      type: "doc",
      id: "cli",
      label: "CLI Reference",
    },
    {
      type: "category",
      label: "Guides",
      collapsed: false,
      items: [
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
        "architecture",
        "github-actions",
        "dependencies",
        "crd-reference",
        "secrets",
        "oauth-tunnels",
      ],
    },
  ],
};

export default sidebars;
