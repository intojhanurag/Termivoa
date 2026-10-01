import { defineRouteMiddleware } from '@astrojs/starlight/route-data';

// Point AI tools at the page's plain-Markdown copy.
export const onRequest = defineRouteMiddleware((context) => {
	const route = context.locals.starlightRoute;
	if (!route.id.startsWith('docs')) return;
	const path = route.id === 'docs' ? 'docs/index' : route.id;
	route.head.push({ tag: 'link', attrs: { rel: 'alternate', type: 'text/markdown', href: `/${path}.md` } });
});
