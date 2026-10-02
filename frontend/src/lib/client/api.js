import { error } from '@sveltejs/kit';

export const API_UNREACHABLE = 'Could not reach the TAM client API';

/**
 * Reads the `detail` message of an API error response (`{detail: "..."}`),
 * falling back to the status text / code when the body is not JSON.
 */
export async function readDetail(res) {
	try {
		const data = await res.clone().json();
		if (data && typeof data.detail === 'string' && data.detail) return data.detail;
	} catch {
		// body was not JSON
	}
	return res.statusText || `Error Code: ${res.status}`;
}

/** Human readable message for anything thrown by fetch/getJSON. */
export function errorMessage(e) {
	if (e && typeof e === 'object') {
		if (e.body && typeof e.body.message === 'string') return e.body.message;
		if (typeof e.message === 'string') return e.message;
	}
	return String(e);
}

/**
 * GET a JSON endpoint. Throws a SvelteKit HttpError carrying the status and the
 * API `detail` on a non-ok response, or a 503 when the API cannot be reached.
 * Pass `{ fetch }` from a `load` function to use SvelteKit's fetch.
 */
export async function getJSON(url, { fetch: doFetch = globalThis.fetch, headers } = {}) {
	let res;
	try {
		res = await doFetch(url, headers ? { headers } : undefined);
	} catch {
		error(503, API_UNREACHABLE);
	}
	if (!res.ok) error(res.status, await readDetail(res));
	return res.json();
}

/**
 * GET a JSON endpoint like getJSON, and say whether the TAM client program
 * answered from its own copy because the server could not be reached
 * (`fromCopy`): reports show that, on screen and on paper.
 */
export async function getReport(url, { fetch: doFetch = globalThis.fetch } = {}) {
	let res;
	try {
		res = await doFetch(url);
	} catch {
		error(503, API_UNREACHABLE);
	}
	if (!res.ok) error(res.status, await readDetail(res));
	return { data: await res.json(), fromCopy: res.headers.get('X-TAM-Copy') === '1' };
}

/**
 * POST a JSON body (always with `Content-Type: application/json`).
 * Returns the raw Response so callers can check `res.ok` and read the body.
 */
export function postJSON(
	url,
	body,
	{ fetch: doFetch = globalThis.fetch, headers = {}, keepalive = false } = {}
) {
	return doFetch(url, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json', ...headers },
		body: JSON.stringify(body),
		// keepalive lets a save started from beforeunload outlive the page.
		keepalive
	});
}

/** What a form says when a save cannot reach the TAM client program. */
export const SAVE_UNREACHABLE =
	'Could not reach the TAM client program. Nothing was saved; your rows are still on this page.';

/**
 * The fields each form saves, and how a message names a row. A save sends
 * each row with the values the volunteer started from (`base`, see
 * startFrom): the server changes a field only while it still holds that
 * value, so a value another computer changed meanwhile is never overwritten
 * by an older one, and the answer shows which changes were not made.
 */
export const TICKET_FORM = {
	fields: [
		['first_name', 'first name'],
		['last_name', 'last name'],
		['phone_number', 'phone number'],
		['pref', 'contact preference']
	],
	name: (r) => `Ticket ${r.t_id}`
};
export const SEARCH_FORM = { ...TICKET_FORM, name: (r) => `Ticket ${r.prefix} ${r.t_id}` };
export const BASKET_FORM = {
	fields: [
		['description', 'description'],
		['donors', 'donors']
	],
	name: (r) => `Basket ${r.b_id}`
};
// An empty Winning Ticket box is 0: no winner.
export const DRAWING_FORM = {
	fields: [['winning_ticket', 'winning ticket']],
	name: (r) => `Basket ${r.b_id}`,
	number: true
};

/** A field's value as the forms compare them. */
function valueOf(form, v) {
	if (v === null || v === undefined || v === '') return form.number ? '0' : '';
	return String(v);
}

/** The values of the form's fields in a row. */
function valuesOf(form, row) {
	const out = {};
	for (const [f] of form.fields) out[f] = row[f] ?? (form.number ? 0 : '');
	return out;
}

/** A value as a message shows it. */
function shown(v) {
	return v === null || v === undefined || v === '' ? '(blank)' : `"${v}"`;
}

/**
 * Remembers on each row the values it was loaded with, which its save
 * sends as the values the volunteer started from. Returns the rows.
 */
export function startFrom(form, rows) {
	for (const r of rows) r.base = valuesOf(form, r);
	return rows;
}

/**
 * A row's values for another row (the duplicate, copy and paste keys): all
 * but what belongs to the row itself, its prefix, number, starting values,
 * order numbers and mark, and the `keys` given.
 */
export function rowValues(row, ...keys) {
	const out = { ...row };
	for (const k of ['prefix', 'base', 'rev', 'win_rev', 'changed', ...keys]) delete out[k];
	return out;
}

/**
 * Saves a form's marked rows (`changed` set) with one POST to `url`.
 * Returns '' when they were saved (or there was nothing to save), otherwise
 * the message to show: the API's own reason, or that the client program
 * could not be reached (it was shut down or crashed); then nothing was
 * saved and the rows stay marked.
 *
 * With `form` (TICKET_FORM and the like) each row goes with the values it
 * was loaded with, and the answer, the rows as stored, comes back onto the
 * page: a change another computer's newer value kept out is listed in the
 * message, and the field shows the value it holds now, to be typed again
 * if the volunteer's is right. `after(row)` runs for each row the answer
 * updated. A row typed in again while the save was on its way keeps its
 * mark and its new typing, so the next save sends what is on the screen.
 */
export async function saveMarked(url, rows, { keepalive = false, form, after } = {}) {
	if (rows.length === 0) return '';
	const sentValues = rows.map((r) => (form ? valuesOf(form, r) : JSON.stringify(r)));
	const sentBase = rows.map((r) => r.base);
	// eslint-disable-next-line no-unused-vars
	const body = rows.map(({ changed, base, ...row }) => (form && base ? { ...row, base } : row));
	let res;
	try {
		res = await postJSON(url, body, { keepalive });
	} catch {
		return SAVE_UNREACHABLE;
	}
	if (!res.ok) {
		const reason = (await readDetail(res)).replace(/[\s.]+$/, '');
		return `Nothing was saved: ${reason}. Your rows are still on this page.`;
	}
	if (!form) {
		rows.forEach((r, i) => {
			if (JSON.stringify(r) === sentValues[i]) r.changed = false;
		});
		return '';
	}
	let stored = null;
	try {
		stored = await res.json();
	} catch {
		// No rows in the answer: the page keeps what it shows.
	}
	const notSaved = [];
	rows.forEach((r, i) => {
		const sent = sentValues[i];
		const typedAgain = form.fields.some(([f]) => valueOf(form, r[f]) !== valueOf(form, sent[f]));
		const now = Array.isArray(stored) ? stored[i] : null;
		if (now) {
			for (const [f, label] of form.fields) {
				const base = sentBase[i]?.[f];
				const changed = base !== undefined && valueOf(form, sent[f]) !== valueOf(form, base);
				if (changed && valueOf(form, now[f]) !== valueOf(form, sent[f])) {
					notSaved.push(`${form.name(r)} ${label}: now ${shown(now[f])}, yours was ${shown(sent[f])}`);
				}
				if (valueOf(form, r[f]) === valueOf(form, sent[f])) r[f] = now[f];
			}
			r.base = valuesOf(form, now);
			for (const k of ['rev', 'win_rev']) if (k in now) r[k] = now[k];
			after?.(r);
		}
		if (!typedAgain) r.changed = false;
	});
	if (notSaved.length > 0) {
		return (
			'Some changes were not saved: another computer changed these after this page loaded.\n\n' +
			notSaved.join('\n') +
			'\n\nThe page now shows the current values. To keep yours, type it again and save.'
		);
	}
	return '';
}

/**
 * Keeps a form's marked rows when the volunteer leaves the page, and returns
 * the function that stops listening. A link on the page saves the marked
 * rows first and is followed once they are saved; when they cannot be
 * saved, or another computer's newer values kept some changes out, the
 * page stays with the message. A tab that is closed runs its unload
 * handlers, but one that is only hidden can end without them: a browser may
 * discard a tab left in the background to free memory, and a phone or
 * tablet may stop it. So `save({ keepalive: true })` runs when the page is
 * hidden as well as when it is closed; keepalive lets the request outlive
 * the page. Closing a tab also hides it, so rows already on their way are
 * not sent a second time.
 *
 * `marked()` gives the rows marked now; `save(opts)` resolves to false when
 * they were not all saved.
 */
export function saveOnLeave(marked, save) {
	let sending = '';
	const leave = () => {
		const rows = marked();
		if (rows.length === 0) return;
		const now = JSON.stringify(rows);
		if (now === sending) return;
		sending = now;
		Promise.resolve(save({ keepalive: true })).finally(() => {
			if (sending === now) sending = '';
		});
	};
	const hidden = () => {
		if (document.visibilityState === 'hidden') leave();
	};
	let following = false;
	const follow = async (e) => {
		const link = e.target.closest?.('a[href]');
		if (!link || e.defaultPrevented || e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
		if (link.target && link.target !== '_self') return;
		const to = new URL(link.href, location.href);
		if (to.origin !== location.origin || marked().length === 0) return;
		e.preventDefault();
		if (following) return;
		following = true;
		try {
			if (await save({})) location.assign(to.href);
		} finally {
			following = false;
		}
	};
	window.addEventListener('beforeunload', leave);
	window.addEventListener('pagehide', leave);
	document.addEventListener('visibilitychange', hidden);
	document.addEventListener('click', follow);
	return () => {
		window.removeEventListener('beforeunload', leave);
		window.removeEventListener('pagehide', leave);
		document.removeEventListener('visibilitychange', hidden);
		document.removeEventListener('click', follow);
	};
}

/**
 * GET a JSON endpoint for polling: never throws. Returns `{ status, data }`
 * with the HTTP status (0 when the client could not be reached) and the parsed
 * body (null when the body is not JSON).
 */
export async function pollJSON(url, { fetch: doFetch = globalThis.fetch } = {}) {
	let res;
	try {
		res = await doFetch(url);
	} catch {
		return { status: 0, data: null };
	}
	let data = null;
	try {
		data = await res.json();
	} catch {
		// body was not JSON
	}
	return { status: res.status, data };
}

/** "1 save" / "2 saves" */
export function saves(n) {
	return `${n} save${n === 1 ? '' : 's'}`;
}
