<script>
	import { prefixPage } from '$lib/client/paths';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';
	import { bAS, bS } from '$lib/client/styles';
	import { getReport, errorMessage } from '$lib/client/api';
	import { untrack, tick } from 'svelte';

	let currentFilter = $state('');
	let { data } = $props();
	let prefix = $derived(data.prefix);

	let pageTitle = $derived(`${prefix.prefix} Winners by Name | TAM`);
	let filterTitle = $state('All Winners');

	const headers = ['Winner Name', 'Phone Number', 'Basket ID', 'Winning Ticket', 'Description'];

	// The report as last read, and whether it came from this computer's
	// copy because the server could not be reached. Print reads it again
	// first, so a page left open does not print winners entered since.
	let report = $state(untrack(() => ({ lines: data.reportLines, fromCopy: data.fromCopy, asOf: data.asOf })));

	async function print() {
		try {
			const fresh = await getReport(`/api/reports/byname/${encodeURIComponent(prefix.prefix)}`);
			report = { lines: fresh.data, fromCopy: fresh.fromCopy, asOf: new Date() };
		} catch (e) {
			const asOf = report.asOf.toLocaleTimeString();
			if (!confirm(`The report could not be read again (${errorMessage(e)}). Print the rows on screen, as of ${asOf}?`)) return;
		}
		await tick();
		window.print();
	}

	let reportLines = $derived.by(() => {
		if (currentFilter == 'CALL') {
			return report.lines.filter((l) => l.pref == 'CALL');
		} else if (currentFilter == 'TEXT') {
			return report.lines.filter((l) => l.pref == 'TEXT');
		} else {
			return report.lines;
		}
	});
</script>

<svelte:head>
	<title>{pageTitle}</title>
</svelte:head>

<table class="w-full box-border border-separate p-1">
	<thead>
		<tr class="print:hidden">
			<td colspan="50">
				<HeaderBar>
					<div>By Name Reports:</div>
					{#each data.prefixes as p (p.prefix)}
						<a
							href={prefixPage('/reports/byname/[prefix]', p.prefix)}
							class={p.prefix == prefix.prefix ? bAS[p.color] : bS[p.color]}>{p.prefix}</a
						>
					{/each}
				</HeaderBar>
				<div class="flex flex-row gap-1 py-1 justify-between">
					<div class="flex flex-row gap-1">
						<button
							class={bS[prefix.color]}
							onclick={() => {
								currentFilter = '';
								filterTitle = 'All Winners';
							}}>All Preferences</button
						>
						<button
							class={bS[prefix.color]}
							onclick={() => {
								currentFilter = 'CALL';
								filterTitle = 'Winners Preferring a CALL';
							}}>Call Preference</button
						>
						<button
							class={bS[prefix.color]}
							onclick={() => {
								currentFilter = 'TEXT';
								filterTitle = 'Winners Preferring a TEXT';
							}}>Text Preference</button
						>
					</div>
					<div class="flex flex-row gap-1">
						<button class={bS[prefix.color]} onclick={print}>Print</button>
					</div>
				</div>
			</td>
		</tr>
		<tr>
			<th colspan="50"><h1 class="text-lg text-left">{pageTitle}</h1></th>
		</tr>
		<tr>
			<th colspan="50"><h2 class="italic text-left">{filterTitle}</h2></th>
		</tr>
		<tr>
			<th colspan="50" class="text-left text-xs font-normal">As of {report.asOf.toLocaleString()}</th>
		</tr>
		{#if report.fromCopy}
			<tr>
				<th colspan="50" class="text-left text-sm font-bold text-red-700">
					From this computer's copy: the server could not be reached. Winners entered on other
					computers since this one last reached the server are not in it.
				</th>
			</tr>
		{/if}
		<tr class="text-sm">
			{#each headers as header (header)}
				<th class="text-left border p-0.5">{header}</th>
			{/each}
		</tr>
	</thead>
	<tbody class="text-sm">
		{#each reportLines as line, idx (idx)}
			<tr class="break-inside-avoid">
				<td class="p-0.5 border">{line.last_name || ''}, {line.first_name || ''}</td>
				<td class="p-0.5 border">{line.phone_number || ''}</td>
				<td class="p-0.5 border">{line.b_id}</td>
				<td class="p-0.5 border">{line.winning_ticket}</td>
				<td class="p-0.5 border">{line.description || ''}</td>
			</tr>
		{/each}
	</tbody>
	<tfoot>
		<tr>
			<td colspan="50" class="text-center text-xs">{data.venueName}</td>
		</tr>
		<tr>
			<td colspan="50" class="text-center text-xs">&copy; 2026 Ticket Auction Manager</td>
		</tr>
	</tfoot>
</table>
