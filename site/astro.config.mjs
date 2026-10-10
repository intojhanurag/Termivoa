// The landing page is plain HTML in public/; Starlight serves the docs under /docs/.
import { defineConfig, passthroughImageService } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightLinksValidator from 'starlight-links-validator';
import starlightLlmsTxt from 'starlight-llms-txt';

export default defineConfig({
	site: 'https://termivoa.com',
	// Docs images are already small WebP files; serve them as they are (no sharp).
	image: { service: passthroughImageService() },
	integrations: [
		starlight({
			title: 'Termivoa Docs',
			description: 'Use your computer’s terminal and coding agents from your phone, privately over Tailscale.',
			logo: { src: './src/assets/mark.svg' },
			favicon: '/assets/mark.svg',
			social: [{ icon: 'github', label: 'Termivoa on GitHub', href: 'https://github.com/intojhanurag/Termivoa' }],
			customCss: ['./src/styles/docs.css'],
			// Each docs page also links its plain-Markdown copy (src/pages/docs/[...slug].md.ts) for AI tools.
			routeMiddleware: './src/routeData.ts',
			plugins: [
				// Fails the build on a broken docs link or #anchor (AGENTS.md rule 23).
				starlightLinksValidator(),
				starlightLlmsTxt({
					projectName: 'Termivoa',
					description:
						'Termivoa lets you use the terminals and coding agents (Claude Code, Codex) on your computer from your phone, privately over Tailscale. No cloud service, no account.',
					details:
						'Status: free and open-source beta (v0.1.0-beta.4). The host program is `pterm`. Platforms: macOS on Apple silicon, Ubuntu Linux, Windows 11.',
					customSets: [
						{ label: 'Install', description: 'install guides for each platform', paths: ['docs/install/**'] },
						{ label: 'Guides', description: 'how to use each feature', paths: ['docs/guides/**'] },
						{ label: 'Reference', description: 'CLI, troubleshooting, security and release notes', paths: ['docs/reference/**'] },
					],
					promote: ['docs', 'docs/quick-start', 'docs/pair-your-phone'],
				}),
			],
			expressiveCode: {
				themes: ['github-dark-default', 'github-light-default'],
				styleOverrides: { borderRadius: '10px' },
			},
			head: [
				{ tag: 'meta', attrs: { property: 'og:image', content: 'https://termivoa.com/assets/og.png' } },
				{ tag: 'meta', attrs: { property: 'og:image:alt', content: 'Termivoa: your terminal and coding agents on your phone, over Tailscale.' } },
				{ tag: 'meta', attrs: { name: 'twitter:card', content: 'summary_large_image' } },
				{ tag: 'meta', attrs: { name: 'twitter:image', content: 'https://termivoa.com/assets/og.png' } },
				// Vercel Web Analytics: page views, no cookies; served from this site (vercel.json CSP).
				{ tag: 'script', attrs: { defer: true, src: '/_vercel/insights/script.js' } },
			],
			sidebar: [
				{
					label: 'Start here',
					items: ['docs', { slug: 'docs/quick-start', label: 'Quick start and downloads' }, 'docs/pair-your-phone'],
				},
				{
					label: 'Install',
					items: ['docs/install/macos', 'docs/install/linux', 'docs/install/windows'],
				},
				{
					label: 'Using Termivoa',
					items: [
						'docs/guides/sessions',
						'docs/guides/wall',
						'docs/guides/computers',
						'docs/guides/recap',
						{ label: 'Live terminal and history', slug: 'docs/guides/live-terminal' },
						'docs/guides/project-buttons',
						'docs/guides/answer-cards',
						'docs/guides/needs-you',
						'docs/guides/changes',
						'docs/guides/files',
						'docs/guides/worktrees',
						'docs/guides/notifications',
						'docs/guides/preview',
						'docs/guides/photo-and-voice',
					],
				},
				{
					label: 'Providers',
					items: ['docs/providers', { label: 'herdr', slug: 'docs/guides/herdr' }],
				},
				{
					label: 'Integrations',
					items: [{ label: 'GitHub', slug: 'docs/guides/github' }],
				},
				{
					label: 'Reference',
					items: [
						'docs/reference/cli',
						'docs/reference/troubleshooting',
						'docs/reference/update-and-uninstall',
						'docs/reference/security',
						'docs/reference/release-notes',
					],
				},
			],
		}),
	],
});
