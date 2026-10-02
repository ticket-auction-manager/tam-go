<script>
	import { untrack } from 'svelte';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';
	import { tS, bS } from '$lib/client/styles';

	let { data } = $props();
	let { prefixes } = $derived(data);
	let tableData = $state([]);
	let currentTimeout = $state();
	let lastRefreshed = $state('');
	let interval = $state('0');
	// Why the last refresh failed; the figures shown stay those of lastRefreshed.
	let problem = $state('');
	let fromCopy = $state(false);

	// alive is cleared when the page goes away so a refresh that was in
	// flight cannot schedule the next one.
	let alive = true;
	// A failed refresh keeps the figures shown and says why; with an interval
	// chosen, the next refresh is tried all the same.
	const loadCounts = async () => {
		clearTimeout(currentTimeout);
		let res;
		try {
			res = await fetch('/api/reports/counts');
		} catch {
			res = null;
		}
		if (!alive) return;
		if (!res) {
			problem = 'Could not reach the TAM client program.';
		} else if (!res.ok) {
			problem = `The counts could not be read (${res.status}).`;
		} else {
			const rtnData = {};
			// The total row is keyed apart: a prefix may be named Total, but no
			// prefix name holds a slash.
			const key = (c) => (c.is_total ? '/total' : c.prefix);
			prefixes.forEach((p) => (rtnData[p.prefix] = { ...p }));
			const resData = await res.json();
			resData.forEach((c) => (rtnData[key(c)] = { ...rtnData[key(c)], ...c, key: key(c) }));
			tableData = [...Object.values(rtnData)];
			fromCopy = res.headers.get('X-TAM-Copy') === '1';
			problem = '';
			lastRefreshed = new Date().toLocaleString();
		}
		if (interval > 0) {
			currentTimeout = setTimeout(loadCounts, interval);
		}
	};

	const pageTitle = 'Ticket Counts | TAM';

	$effect(() => {
		alive = true;
		untrack(() => loadCounts());
		return () => {
			alive = false;
			clearTimeout(currentTimeout);
		};
	});
</script>

<svelte:head>
	<title>{pageTitle}</title>
</svelte:head>

<div id="app-container" class="p-1">
	<HeaderBar></HeaderBar>
	<h1 class="text-xl font-bold">{pageTitle}</h1>
	<table class="border-separate box-border w-full">
		<thead>
			<tr>
				<th class="border p-0.5">Prefix</th>
				<th class="border p-0.5">Unique Buyers</th>
				<th class="border p-0.5">Total Buys</th>
			</tr>
		</thead>
		<tbody>
			{#each tableData as line (line.key ?? line.prefix)}
				<tr class={tS[line.color] || ''}>
					<td class="border p-0.5">{line.prefix}</td>
					<td class="border p-0.5">{line.unique_buyers || 0}</td>
					<td class="border p-0.5">{line.total_buys || 0}</td>
				</tr>
			{/each}
		</tbody>
	</table>
	<div class="flex flex-row gap-1 py-1 items-center">
		<select id="interval_select" class="border p-1" bind:value={interval}>
			<option value="0">No Interval</option>
			<option value="30000">30 sec</option>
			<option value="60000">1 Min</option>
			<option value="120000">2 Min</option>
		</select>
		<button class={bS.gray} onclick={() => loadCounts()}
			>Refresh{interval > 0 ? ` Every ${interval / 60000} Min` : ''}</button
		>
		<div>Last refreshed: {lastRefreshed}</div>
	</div>
	{#if problem}
		<p class="text-red-700">{problem} The figures shown are from {lastRefreshed || 'no refresh yet'}.</p>
	{/if}
	{#if fromCopy}
		<p class="text-red-700 font-bold">
			From this computer's copy: the server could not be reached. Tickets entered on other computers
			since this one last reached the server are not counted.
		</p>
	{/if}
</div>
