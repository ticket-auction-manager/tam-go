<script>
	import logo from '$lib/assets/logo.svg';
	import { tS, bS, bAS } from '$lib/client/styles.js';
	import { resolve } from '$app/paths';
	import { prefixPage } from '$lib/client/paths.js';
	import hotkeys from 'hotkeys-js';

	const pageTitle = 'Main Menu | TAM';
	const { data } = $props();
	let adminMode = $state(false);
	let stopped = $state(false);
	let shutdownError = $state('');

	async function shutdown() {
		if (
			!confirm(
				'Stop the TAM client on this computer? The pages will stop working until it is started again.'
			)
		)
			return;
		let res;
		try {
			res = await fetch('/api/shutdown', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: '{}'
			});
		} catch {
			shutdownError = 'Could not reach the TAM client.';
			return;
		}
		if (res.ok) {
			stopped = true;
		} else {
			let detail = `Error Code: ${res.status}`;
			try {
				detail = (await res.json()).detail || detail;
			} catch {
				// keep the status text
			}
			shutdownError = detail;
		}
	}
	let prefixes = $derived(data.prefixes);
	let curPrefix = $state('');
	let pColor = $derived.by(() => {
		if (curPrefix) return prefixes.find((p) => curPrefix == p.prefix)?.color || 'gray';
		else return 'gray';
	});

	const status = $derived.by(() => {
		if (data.whoami === 'TAM Server') {
			return {
				mode: 'Remote',
				auth: data.authenticated ? 'green' : 'red',
				healthy: data.healthy ? 'green' : 'red'
			};
		} else if (data.whoami === 'TAM Client') {
			return {
				mode: 'Standalone'
			};
		} else {
			return {
				mode: 'Unknown'
			};
		}
	});

	$effect(() => {
		hotkeys.filter = () => {
			return true;
		};
		const toggleAdminMode = (event) => {
			event.preventDefault();
			adminMode = !adminMode;
		};
		hotkeys('alt+a', toggleAdminMode);
		return () => {
			hotkeys.unbind('alt+a', toggleAdminMode);
		};
	});
</script>

<svelte:head>
	<title>{pageTitle}</title>
</svelte:head>

<div class="p-1" id="app_container">
	<div class="flex flex-row gap-1 items-center">
		<div>
			<img src={logo} alt="TAM Logo" style="height: 1.75rem" />
		</div>
		<div>
			<h1 class="text-xl font-bold">{pageTitle}</h1>
			{#if data.error}
				<p class={tS.red}>{data.error}</p>
			{/if}
			<p class="text-lg italic">{data.venueName}</p>
		</div>
	</div>

	<div class="flex flex-col md:flex-row md:flex-wrap gap-1 py-1">
		<div id="prefixes" class="flex flex-col gap-1 p-2 border border-black rounded">
			<h2 class="text-lg font-bold">Prefix Selection:</h2>
			{#each prefixes as prefix (prefix.prefix)}
				<button
					class={curPrefix == prefix.prefix ? bAS[prefix.color] : bS[prefix.color]}
					onclick={() => (curPrefix = prefix.prefix)}>{prefix.prefix}</button
				>
			{:else}
				<div>No Prefixes</div>
			{/each}
		</div>
		{#if curPrefix}
			<div class="flex flex-col gap-1 items-center p-1 border border-black rounded">
				<h2 class="text-lg font-bold">Forms:</h2>
				<div class="grid grid-cols-2 gap-1 p-1 text-center">
					<a href={prefixPage('/tickets/[prefix]', curPrefix)} class={bS[pColor]}>Tickets</a>
					<a href={prefixPage('/baskets/[prefix]', curPrefix)} class={bS[pColor]}>Baskets</a>
					<a href={prefixPage('/drawing/[prefix]', curPrefix)} class="{bS[pColor]} col-span-2"
						>Drawing Form</a
					>
				</div>
			</div>
			<div class="flex flex-col gap-1 items-center p-1 border border-black rounded">
				<h2 class="text-lg font-bold">Reports:</h2>
				<div class="grid grid-cols-2 gap-1 p-1 text-center">
					<a href={prefixPage('/reports/byname/[prefix]', curPrefix)} class={bS[pColor]}
						>Winners By Name</a
					>
					<a href={prefixPage('/reports/bybasket/[prefix]', curPrefix)} class={bS[pColor]}
						>Winners By Basket</a
					>
				</div>
			</div>
		{:else}
			<div class="flex flex-col gap-1 items-center justify-center p-2 border border-black rounded">
				<h2 class="text-lg font-bold">Please select a prefix to continue.</h2>
			</div>
		{/if}
		<div class="flex flex-col gap-1 items-center text-center p-1 border border-black rounded">
			<h2 class="text-lg font-bold">Prefix Independent:</h2>
			<a href={resolve('/reports/counts')} class="{bS.gray} w-full">Ticket Counts</a>
			<a href={resolve('/sheets')} class="{bS.gray} w-full">Print Sheets</a>
		</div>
	</div>

	{#if adminMode}
		<div id="admin_mode" class="py-1">
			<h2 class="text-lg font-bold">Admin Mode:</h2>
			<div class="flex flex-row gap-1">
				<a href={resolve('/settings')} class={bS.gray}>Settings</a>
				<a href={resolve('/search/tickets')} class={bS.gray}>Search Tickets</a>
				<button class={bS.red} onclick={shutdown} disabled={stopped}>Shut Down TAM</button>
			</div>
			{#if stopped}
				<p class="py-1 font-bold">
					The TAM client has stopped. You can close this tab; start the program again to continue.
				</p>
			{:else if shutdownError}
				<p class="py-1 {tS.red}">{shutdownError}</p>
			{/if}
		</div>
	{/if}

	<div id="footer">
		<div>Mode: {status.mode}</div>
		{#if data.authenticated !== undefined}
			<div>
				Authenticated:
				{#if data.healthy === false}
					<!-- A server that cannot be reached cannot say whether it takes this client's key. -->
					<span class={tS.gray}>unknown</span>
				{:else}
					<span class={tS[status.auth]}>{data.authenticated ? 'Yes' : 'No'}</span>
				{/if}
			</div>
		{/if}
		{#if data.healthy !== undefined}
			<div>
				Server Healthy: <span class={tS[status.healthy]}>{data.healthy ? 'Yes' : 'No'}</span>
			</div>
		{/if}
		<div class="text-center text-xs">
			<p>&copy; 2026 Ticket Auction Manager</p>
			{#if !data.disableAttrib}
				<p>
					Created by Dilan Gilluly and Jacob Burrows. More information on <a href={resolve('/credits')} class="text-blue-600">credits page.</a>
				</p>
			{/if}
		</div>
	</div>
</div>
