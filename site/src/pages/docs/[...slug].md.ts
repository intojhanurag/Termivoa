// A plain-Markdown copy of each docs page, for AI tools: /docs/<page>.md (the start page is /docs/index.md).
import type { APIRoute, GetStaticPaths } from 'astro';
import { getCollection, type CollectionEntry } from 'astro:content';

export const getStaticPaths: GetStaticPaths = async () =>
	(await getCollection('docs')).map((entry) => ({
		params: { slug: markdownSlug(entry.id) },
		props: { entry },
	}));

function markdownSlug(id: string): string {
	return id === 'docs' ? 'index' : id.replace(/^docs\//, '');
}

export const GET: APIRoute = ({ props }) => {
	const { entry } = props as { entry: CollectionEntry<'docs'> };
	const body = `# ${entry.data.title}\n\n> ${entry.data.description ?? ''}\n\n${entry.body ?? ''}`;
	return new Response(body, { headers: { 'Content-Type': 'text/markdown; charset=utf-8' } });
};
