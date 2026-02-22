import { themes as prismThemes } from "prism-react-renderer";
import type { Config } from "@docusaurus/types";
import type * as Preset from "@docusaurus/preset-classic";

const config: Config = {
  title: "kindling",
  tagline: "Push code. Your laptop builds it. Your laptop runs it.",
  favicon: "img/favicon.svg",

  url: "https://kindling.sh",
  baseUrl: "/",

  organizationName: "kindling-sh",
  projectName: "kindling",
  deploymentBranch: "gh-pages",
  trailingSlash: false,

  onBrokenLinks: "throw",
  onBrokenMarkdownLinks: "warn",

  i18n: {
    defaultLocale: "en",
    locales: ["en"],
  },

  markdown: {
    mermaid: true,
  },
  themes: ["@docusaurus/theme-mermaid"],

  presets: [
    [
      "classic",
      {
        docs: {
          sidebarPath: "./sidebars.ts",
          editUrl: "https://github.com/kindling-sh/kindling/tree/main/docs-site/",
        },
        blog: false,
        theme: {
          customCss: "./src/css/custom.css",
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    image: "img/kindling-social-card.png",
    colorMode: {
      defaultMode: "dark",
      disableSwitch: false,
      respectPrefersColorScheme: false,
    },
    navbar: {
      title: "kindling",
      logo: {
        alt: "kindling logo",
        src: "img/logo.svg",
      },
      items: [
        {
          type: "docSidebar",
          sidebarId: "docsSidebar",
          position: "left",
          label: "Docs",
        },
        {
          href: "https://github.com/kindling-sh/kindling",
          label: "GitHub",
          position: "right",
        },
      ],
    },
    footer: {
      style: "dark",
      links: [
        {
          title: "Documentation",
          items: [
            { label: "Quickstart", to: "/docs/quickstart" },
            { label: "CLI Reference", to: "/docs/cli" },
            { label: "Architecture", to: "/docs/architecture" },
          ],
        },
        {
          title: "Resources",
          items: [
            { label: "GitHub Actions", to: "/docs/github-actions" },
            { label: "Dependencies", to: "/docs/dependencies" },
            { label: "CRD Reference", to: "/docs/crd-reference" },
          ],
        },
        {
          title: "Community",
          items: [
            {
              label: "GitHub",
              href: "https://github.com/kindling-sh/kindling",
            },
            {
              label: "Issues",
              href: "https://github.com/kindling-sh/kindling/issues",
            },
            {
              label: "Discussions",
              href: "https://github.com/kindling-sh/kindling/discussions",
            },
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} The kindling Authors. Apache 2.0 License.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ["bash", "yaml", "go", "python", "typescript"],
    },
    mermaid: {
      theme: { light: "neutral", dark: "dark" },
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
