// The landing page is plain HTML in public/; Starlight serves the docs under /docs/.
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
	site: 'https://termivoa.vercel.app',
	trailingSlash: 'ignore',
	integrations: [
		starlight({
			title: 'Termivoa Docs',
			description: 'Use your computer’s terminal and coding agents from your phone, privately over Tailscale.',
			logo: { src: './src/assets/mark.svg' },
			favicon: '/assets/mark.svg',
			customCss: ['./src/styles/docs.css'],
			expressiveCode: {
				themes: ['github-dark-default', 'github-light-default'],
				styleOverrides: { borderRadius: '10px' },
			},
			head: [
				{ tag: 'meta', attrs: { property: 'og:image', content: 'https://termivoa.vercel.app/assets/og.png' } },
			],
			sidebar: [
				{
					label: 'Start here',
					items: ['docs', 'docs/quick-start', 'docs/pair-your-phone'],
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
						'docs/guides/recap',
						'docs/guides/live-terminal',
						'docs/guides/project-buttons',
						'docs/guides/answer-cards',
						'docs/guides/changes',
						'docs/guides/worktrees',
						'docs/guides/notifications',
						'docs/guides/preview',
						'docs/guides/photo-and-voice',
					],
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
