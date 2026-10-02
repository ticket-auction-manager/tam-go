<script>
	import { resolve } from '$app/paths';
	import { pollJSON, saves } from '../api';

	// Where the TAM client program stands, from GET /api/status: whether it
	// answers at all, its connection to the server in remote mode, and a
	// settings file it could not read (in any mode). Nothing is shown in
	// standalone mode while all is well. The bar is never printed.
	let status = $state(null);
	// The program did not answer: it was shut down or crashed.
	let unreachable = $state(false);

	const POLL_MS = 3000;

	$effect(() => {
		let stopped = false;
		let timer;
		const poll = async () => {
			const { status: code, data } = await pollJSON('/api/status');
			if (stopped) return;
			unreachable = code === 0;
			status = code === 200 && data ? data : null;
			timer = setTimeout(poll, POLL_MS);
		};
		poll();
		return () => {
			stopped = true;
			clearTimeout(timer);
		};
	});

	const colors = {
		green: 'bg-green-200 text-green-900 border-green-600',
		amber: 'bg-amber-200 text-amber-900 border-amber-600',
		red: 'bg-red-200 text-red-900 border-red-600',
		gray: 'bg-gray-200 text-gray-900 border-gray-600'
	};

	const waiting = (n) => (n > 0 ? `, ${saves(n)} waiting` : '');

	// The connection line: its colour, its text, and for the states only
	// Settings can fix, a link there followed by `after`.
	let view = $derived.by(() => {
		if (unreachable) {
			return { color: 'red', text: 'TAM client not running: changes cannot be saved' };
		}
		if (!status || status.mode !== 'remote') return null;
		const pending = Number(status.pending) || 0;
		const server = status.server_name || status.server || 'server';
		switch (status.state) {
			case 'connected':
				return {
					color: 'green',
					text: `Connected to ${server}${pending > 0 ? `, sending ${saves(pending)}` : ''}`
				};
			case 'reconnecting':
				return { color: 'amber', text: `Reconnecting${waiting(pending)}` };
			case 'offline':
				return { color: 'red', text: `Offline${waiting(pending)}` };
			case 'unauthenticated':
				return {
					color: 'red',
					text: `The server rejected this client's key${waiting(pending)}: open`,
					settings: 'and pair again'
				};
			case 'certificate':
				return {
					color: 'red',
					text: `The server's certificate changed${waiting(pending)}: open`,
					settings: 'and pair again'
				};
			default:
				return { color: 'gray', text: String(status.state || 'Unknown state') };
		}
	});

	let failed = $derived((!unreachable && status && Number(status.failed)) || 0);
	// settings.json could not be read: the client runs on its last good copy or on defaults.
	let settingsError = $derived(
		(!unreachable && status && typeof status.settings_error === 'string' && status.settings_error) ||
			''
	);
</script>

{#if view || failed > 0 || settingsError}
	<div id="status_bar" role="status" class="w-full text-sm print:hidden">
		{#if view || failed > 0}
			<div class="w-full truncate border-b px-2 py-0.5 {colors[view ? view.color : 'red']}">
				{#if view}
					<span>
						{view.text}
						{#if view.settings}<a href={resolve('/settings')} class="font-bold underline"
								>Settings</a
							>
							{view.settings}{/if}
					</span>
				{/if}
				{#if failed > 0}
					<span class="font-bold">
						{#if view}&middot;{/if}
						{saves(failed)} could not be sent, open
						<a href={resolve('/settings')} class="underline">Settings</a>
					</span>
				{/if}
			</div>
		{/if}
		{#if settingsError}
			<div class="w-full border-b px-2 py-0.5 break-words {colors.red}">{settingsError}</div>
		{/if}
	</div>
{/if}
