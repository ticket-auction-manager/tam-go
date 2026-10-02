<script>
	import { untrack } from 'svelte';
	import { resolve } from '$app/paths';
	import { invalidateAll } from '$app/navigation';
	import { bS, iS, tS } from '$lib/client/styles';
	import { postJSON, pollJSON, readDetail, saves, API_UNREACHABLE } from '$lib/client/api';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';

	let { data } = $props();
	let loadError = $derived(data.loadError || '');
	// Editable working copy of the loaded settings (intentionally captured once).
	let settings = $state(untrack(() => ({ ...data.settings })));
	let status = $state({
		message: '',
		color: 'green'
	});

	const pageTitle = 'Settings | TAM';

	let reloadTimer;
	$effect(() => () => clearTimeout(reloadTimer));

	// --- Server section: pairing, discovered servers and the failed saves ---
	const SERVERS_POLL_MS = 5000;
	const STATUS_POLL_MS = 5000;

	// A server is set either by pairing, which also names it, or by typing
	// it into the Remote Mode fields below, the original's way, where the
	// key comes from Auth Keys. Only the first is "paired".
	let configured = $derived(!!data.settings.remote_server);
	let paired = $derived(configured && !!data.settings.remote_name);
	let pairedName = $derived(data.settings.remote_name || data.settings.remote_server);
	let serverAddress = $derived(`${data.settings.remote_server}:${data.settings.remote_port}`);
	let pairVerb = $derived(paired ? 'Pair again' : 'Pair');
	let servers = $state([]);
	// Fields for pairing; a discovered server's Use button fills them, and
	// while paired they hold the current server, for pairing again.
	let pair = $state(untrack(() => pairFields(data.settings)));
	let busy = $state(false);
	let serverMsg = $state({ message: '', color: 'green' });
	// Saves the server rejected, and saves still waiting to reach it, from
	// GET /api/status (0 in standalone mode).
	let failed = $state(0);
	let pending = $state(0);
	// Why each failed save is in the failed list, from GET /api/outbox/failed:
	// for a change another computer's newer value kept out, the record, the
	// field and both values.
	let failedList = $state([]);
	// The connection state, from GET /api/status ('' in standalone mode).
	let connState = $state('');
	// A refused key and a changed certificate are both fixed by pairing again.
	let mustPairAgain = $derived(connState === 'unauthenticated' || connState === 'certificate');

	function pairFields(s) {
		return {
			host: s.remote_server || '',
			port: s.remote_port || '8000',
			tls: !!s.remote_tls,
			password: ''
		};
	}

	function say(message, color = 'green') {
		serverMsg = { message, color };
	}

	// POST to one of the pairing/outbox routes; returns the answer's message or the error.
	async function post(url, body) {
		let res;
		try {
			res = await postJSON(url, body);
		} catch {
			return { ok: false, message: API_UNREACHABLE };
		}
		if (!res.ok) return { ok: false, message: await readDetail(res) };
		let answer = {};
		try {
			answer = await res.json();
		} catch {
			// no body
		}
		return { ok: true, message: (answer && answer.message) || 'Done' };
	}

	// Re-runs the page's load so the pairing state and the form show the saved settings.
	async function reloadSettings() {
		await invalidateAll();
		settings = { ...data.settings };
		pair = pairFields(data.settings);
	}

	function useServer(s) {
		pair.host = s.host || s.name || '';
		pair.port = String(s.port || (s.tls ? '8443' : '8000'));
		pair.tls = !!s.tls;
	}

	async function doPair() {
		if (busy) return;
		const host = String(pair.host || '').trim();
		const port = String(pair.port || '').trim();
		if (!host) return say('Enter the server host or pick one from the list', 'red');
		if (!port) return say('Enter the server port', 'red');
		busy = true;
		// Pairing again with the server this client is paired with keeps the
		// saves still waiting for it; the client sends them once paired.
		const r = await post('/api/pair', { host, port, tls: !!pair.tls, password: pair.password });
		busy = false;
		// The whole answer: it may also say what became of this client's own data.
		say(r.message, r.ok ? 'green' : 'red');
		if (r.ok) {
			pair.password = '';
			await reloadSettings();
			await pollStatus();
		}
	}

	async function doUnpair() {
		if (busy) return;
		// After pairing, the client's own copy is the server's data; what the
		// client had not sent yet goes to the failed list (see the client's unpair).
		const now = pending > 0 ? ` (${pending} now)` : '';
		if (
			!confirm(
				`Unpair from ${pairedName}? This client goes back to standalone mode and keeps the copy of the server's data it has now. Saves still waiting to reach the server${now} are set aside in the failed list.`
			)
		)
			return;
		busy = true;
		const r = await post('/api/unpair', {});
		busy = false;
		say(r.message, r.ok ? 'green' : 'red');
		if (r.ok) await reloadSettings();
	}

	async function retryFailed() {
		if (busy) return;
		busy = true;
		const r = await post('/api/outbox/retry', {});
		busy = false;
		say(r.message, r.ok ? 'green' : 'red');
		await pollStatus();
	}

	async function discardFailed() {
		if (busy) return;
		if (
			!confirm(
				`Discard the ${saves(failed)} that could not be sent? They will not reach the server.`
			)
		)
			return;
		busy = true;
		const r = await post('/api/outbox/discard', {});
		busy = false;
		say(r.message, r.ok ? 'green' : 'red');
		await pollStatus();
	}

	async function pollStatus() {
		const { status: code, data: s } = await pollJSON('/api/status');
		const remote = code === 200 && s && s.mode === 'remote';
		failed = remote ? Number(s.failed) || 0 : 0;
		pending = remote ? Number(s.pending) || 0 : 0;
		connState = remote ? String(s.state || '') : '';
		if (failed > 0) {
			const { status, data: list } = await pollJSON('/api/outbox/failed');
			if (status === 200 && Array.isArray(list)) failedList = list;
		} else {
			failedList = [];
		}
		return code;
	}

	// The connection state, for Pair again and the "could not be sent" line, while the page is open.
	$effect(() => {
		let stopped = false;
		let timer;
		const loop = async () => {
			await pollStatus();
			if (stopped) return;
			timer = setTimeout(loop, STATUS_POLL_MS);
		};
		loop();
		return () => {
			stopped = true;
			clearTimeout(timer);
		};
	});

	// The servers found on the network, while no server is set.
	$effect(() => {
		if (configured) return;
		let stopped = false;
		let timer;
		const loop = async () => {
			const { status: code, data: list } = await pollJSON('/api/servers');
			if (stopped) return;
			if (code === 200 && Array.isArray(list)) servers = list;
			timer = setTimeout(loop, SERVERS_POLL_MS);
		};
		loop();
		return () => {
			stopped = true;
			clearTimeout(timer);
		};
	});
</script>

<svelte:head>
	<title>{pageTitle}</title>
</svelte:head>

<!-- The pairing form: for a first pairing, and while paired for pairing again. -->
{#snippet pairForm(label)}
	<div class="flex flex-row gap-1 items-center">
		<label for="pair_host">Host:</label>
		<input type="text" id="pair_host" class={iS.normal} bind:value={pair.host} />
	</div>
	<div class="flex flex-row gap-1 items-center">
		<label for="pair_port">Port:</label>
		<input type="text" id="pair_port" class={iS.normal} bind:value={pair.port} />
	</div>
	<div class="flex flex-row gap-1 items-center">
		<div>TLS:</div>
		<button
			class={bS.gray}
			onclick={() => {
				pair.tls = !pair.tls;
				pair.port = pair.tls ? '8443' : '8000';
			}}>{pair.tls ? 'Yes' : 'No'}</button
		>
	</div>
	<div class="flex flex-row gap-1 items-center">
		<label for="pair_password">Server password:</label>
		<input
			type="password"
			id="pair_password"
			autocomplete="off"
			class={iS.normal}
			onkeydown={(e) => {
				if (e.key == 'Enter') doPair();
			}}
			bind:value={pair.password}
		/>
	</div>
	<div class="flex flex-row gap-1 items-center">
		<button
			class="{bS.gray} disabled:opacity-50 disabled:cursor-not-allowed"
			disabled={busy}
			onclick={doPair}>{label}</button
		>
	</div>
{/snippet}

<div id="app_container" class="p-1">
	<HeaderBar>
		<div>Settings Sections:</div>
		{#if data.settings.remote_server}
			<a href={resolve('/settings/auth-keys')} class={bS.gray}>Auth Keys</a>
		{/if}
		<a href={resolve('/settings/prefixes')} class={bS.gray}>Prefixes</a>
		<a href={resolve('/settings/backuprestore')} class={bS.gray}>Backup/Restore</a>
	</HeaderBar>
	<h1 class="text-xl font-bold">{pageTitle}</h1>
	<div id="server_section" class="flex flex-col gap-1 w-full py-1">
		<h2 class="text-lg font-bold">Server:</h2>
		{#if configured}
			<div class="flex flex-row gap-1 items-center">
				<div>
					{#if paired}
						Paired with <span class="font-bold">{pairedName}</span>
						({serverAddress}, TLS {data.settings.remote_tls ? 'on' : 'off'})
					{:else}
						Remote server <span class="font-bold">{serverAddress}</span>
						(TLS {data.settings.remote_tls ? 'on' : 'off'}), set in the Remote Mode fields below,
						not paired{data.settings.remote_key ? '; it uses the key chosen under Auth Keys' : ''}
					{/if}
				</div>
				<button
					class="{bS.red} disabled:opacity-50 disabled:cursor-not-allowed"
					disabled={busy}
					onclick={doUnpair}>Unpair</button
				>
			</div>
			<div
				id="pair_again"
				class="flex flex-col gap-1 self-start max-w-3xl {mustPairAgain
					? 'p-2 border-2 border-red-600 rounded bg-red-50'
					: ''}"
			>
				{#if connState === 'certificate'}
					<p class="{tS.red} font-bold">
						The server's certificate changed since this client paired with it. If the server was
						set up again or given a new certificate, {pairVerb.toLowerCase()} with the server password
						to trust the new one; the saves waiting stay queued and are sent once paired.
					</p>
				{:else if connState === 'unauthenticated'}
					<p class="{tS.red} font-bold">
						The server refused this client's key. {pairVerb} with the server password: the saves
						waiting stay queued and are sent once paired.
					</p>
				{:else if paired}
					<div>
						Pair again with the server password when the server refuses this client's key, its
						certificate changed, or it moved to another address:
					</div>
				{:else}
					<div>Pair with the server password to give this client a key of its own:</div>
				{/if}
				{@render pairForm(pairVerb)}
			</div>
		{:else}
			<div>Servers on this network:</div>
			{#each servers as s}
				<div class="flex flex-row gap-1 items-center">
					<div>
						<span class="font-bold">{s.name || s.host}</span>
						{s.host}:{s.port} (TLS {s.tls ? 'on' : 'off'}{s.version ? `, v${s.version}` : ''})
					</div>
					<button class={bS.gray} onclick={() => useServer(s)}>Use</button>
				</div>
			{:else}
				<div class="italic">Looking for servers on this network...</div>
			{/each}
			{@render pairForm('Pair')}
		{/if}
		{#if failed > 0}
			<ul class="list-disc ml-6 text-sm">
				{#each failedList as f, i (i)}
					<li class="break-words">{f.reason || f.save}</li>
				{/each}
			</ul>
			<div class="flex flex-row gap-1 items-center">
				<div class={tS.red}>{saves(failed)} could not be sent</div>
				<button
					class="{bS.gray} disabled:opacity-50 disabled:cursor-not-allowed"
					disabled={busy}
					onclick={retryFailed}>Retry</button
				>
				<button
					class="{bS.red} disabled:opacity-50 disabled:cursor-not-allowed"
					disabled={busy}
					onclick={discardFailed}>Discard</button
				>
			</div>
		{/if}
		{#if serverMsg.message}
			<p class="{tS[serverMsg.color]} break-words">{serverMsg.message}</p>
		{/if}
	</div>
	<div class="flex flex-col gap-1 w-full py-1">
		<h2 class="text-lg font-bold">Remote Mode:</h2>
		<div class="flex flex-row gap-1 items-center">
			<div>Remote Server:</div>
			<input type="text" id="remote_server" class={iS.normal} bind:value={settings.remote_server} />
		</div>
		<div class="flex flex-row gap-1 items-center">
			<div>Remote Port:</div>
			<input type="text" id="remote_port" class={iS.normal} bind:value={settings.remote_port} />
		</div>
		<div class="flex flex-row gap-1 items-center">
			<div>Remote TLS:</div>
			<button
				class={bS.gray}
				onclick={() => {
					if (settings.remote_tls) {
						settings.remote_tls = false;
						settings.remote_port = '8000';
					} else if (!settings.remote_tls) {
						settings.remote_tls = true;
						settings.remote_port = '8443';
					}
				}}>{settings.remote_tls ? 'Yes' : 'No'}</button
			>
		</div>
		<h2 class="text-lg font-bold">Default Preferences:</h2>
		<div class="flex flex-row gap-1 items-center">
			<div>Contact Preference:</div>
			<button
				class={bS.gray}
				onclick={() => {
					settings.default_pref === 'CALL'
						? (settings.default_pref = 'TEXT')
						: (settings.default_pref = 'CALL');
				}}>{settings.default_pref}</button
			>
		</div>
		<div class="flex flex-row gap-1 items-center">
			<div>Venue Name:</div>
			<input type="text" id="venue_name" class={iS.normal} bind:value={settings.venue_name} />
		</div>
		<div class="flex flex-row gap-1 items-center">
			<div>Disable Attribution:</div>
			<button
				class={bS.gray}
				onclick={() => {
					settings.disable_attrib
						? (settings.disable_attrib = false)
						: (settings.disable_attrib = true);
				}}>{settings.disable_attrib ? 'Yes' : 'No'}</button
			>
		</div>
		<div class="flex flex-row gap-1 items-center">
			<button
				class="{bS.gray} disabled:opacity-50 disabled:cursor-not-allowed"
				disabled={!!loadError}
				onclick={async () => {
					if (loadError) return;
					let res;
					try {
						res = await postJSON('/api/settings', settings);
					} catch {
						status.message = 'Could not reach the TAM client API';
						status.color = 'red';
						return;
					}
					if (!res.ok) {
						status.message = `Error Code: ${res.status} (${await readDetail(res)})`;
						status.color = 'red';
					} else {
						const resData = await res.json();
						// The Remote Mode fields set where the server is; they do not pair.
						const remoteChanged = ['remote_server', 'remote_port', 'remote_tls'].some(
							(k) => resData[k] !== data.settings[k]
						);
						settings = { ...resData };
						status.message = remoteChanged
							? 'Remote server settings saved.'
							: 'Settings saved successfully!';
						status.color = 'green';
						clearTimeout(reloadTimer);
						reloadTimer = setTimeout(() => window.location.reload(), 3000);
					}
				}}>Save</button
			>
			<button
				class={bS.gray}
				onclick={() => {
					settings = { ...data.settings };
				}}>Cancel</button
			>
		</div>
		<div>
			<p class={tS[loadError ? 'red' : status.color]}>{loadError || status.message}</p>
		</div>
	</div>
</div>
