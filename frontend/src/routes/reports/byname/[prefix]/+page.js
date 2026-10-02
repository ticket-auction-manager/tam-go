import { getJSON, getReport } from '$lib/client/api';

export const load = async ({ params, fetch }) => {
	const [prefixes, report, settings] = await Promise.all([
		getJSON('/api/prefixes', { fetch }),
		getReport(`/api/reports/byname/${encodeURIComponent(params.prefix)}`, { fetch }),
		getJSON('/api/settings', { fetch })
	]);
	const prefix = Array.from(prefixes).find((p) => p.prefix == params.prefix) || {
		prefix: params.prefix,
		color: 'gray',
		weight: 0
	};
	return {
		prefixes,
		prefix,
		reportLines: report.data,
		fromCopy: report.fromCopy,
		asOf: new Date(),
		venueName: settings.venue_name || ''
	};
};
