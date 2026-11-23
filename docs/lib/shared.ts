export const appName = 'Mikrozia';
export const appTagline = 'Control panel for Xray-core on MikroTik RouterOS Container';

export const docsRoute = '/docs';
export const docsImageRoute = '/og/docs';
export const docsContentRoute = '/llms.mdx/docs';

// The Mikrozia product repository — used for the navbar GitHub link,
// build-time star/release stats, and install commands.
export const productRepo = {
  user: 'EchoDev7',
  repo: 'Mikrozia',
  branch: 'main',
};

// Where these docs live in the Mikrozia monorepo — used for "Edit on GitHub" links.
export const gitConfig = {
  user: 'EchoDev7',
  repo: 'Mikrozia',
  branch: 'main',
  docsDir: 'docs/content/docs',
};

export const productRepoUrl = `https://github.com/${productRepo.user}/${productRepo.repo}`;

// AI-generated interactive wiki of the Mikrozia codebase.
export const deepWikiUrl = `https://deepwiki.com/${productRepo.user}/${productRepo.repo}`;

// Official Mikrozia community channel on Telegram (announcements & support).
export const telegramChannel = 'Mikrozia';
export const telegramChannelUrl = `https://t.me/${telegramChannel}`;

// Public site origin, used for metadataBase / canonical URLs / OG images.
// Defaults to the GitHub Pages origin, so the env var is optional. Use `||` (not
// `??`) so an empty string — e.g. an unset `${{ vars.NEXT_PUBLIC_SITE_URL }}`
// in CI — also falls back instead of shipping a blank origin.
export const siteUrl = process.env.NEXT_PUBLIC_SITE_URL || 'https://echodev7.github.io/Mikrozia';
export const basePath = process.env.NEXT_PUBLIC_BASE_PATH || '';
