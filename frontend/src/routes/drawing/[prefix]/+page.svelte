<script>
	import { prefixPage } from '$lib/client/paths';
	import { bS, bAS, iS, rBS } from '$lib/client/styles';
	import { getJSON, saveMarked, saveOnLeave, errorMessage, startFrom, rowValues, DRAWING_FORM } from '$lib/client/api';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';
	import PagerBar from '$lib/client/components/PagerBar.svelte';
	import CommandBar from '$lib/client/components/CommandBar.svelte';

	let { data } = $props();
	let { prefix, prefixes } = $derived(data);

	let pageTitle = $derived(`${prefix.prefix} Drawing Form | TAM`);

	let curIdx = $state(0),
		nextIdx = $derived(curIdx + 1),
		prevIdx = $derived(curIdx - 1);
	const changeIdx = (idx) => {
		curIdx = idx;
	};
	const focusIdx = (idx) => {
		curIdx = idx;
		const elemIdx = document.getElementById(`${idx}_first`);
		if (elemIdx) {
			elemIdx.select();
		}
	};

	// Shows the winner of the number in a row's Winning Ticket box. Every
	// keystroke asks, and answers can come back out of order (a slow link to
	// the server): an answer for a number no longer in the box is dropped, so
	// the lookup of 1 cannot replace the winner of 123. An empty box, or one
	// that holds no ticket number, has no winner and is not looked up.
	async function showWinner(item) {
		const wanted = item.winning_ticket;
		let ticket = null;
		if (Number.isInteger(wanted) && wanted >= 0) {
			try {
				ticket = await getJSON(`/api/tickets/${encodeURIComponent(prefix.prefix)}/${wanted}`);
			} catch {
				// No winner shown when the lookup fails.
			}
			if (item.winning_ticket !== wanted) return;
		}
		[item.last_name, item.first_name, item.phone_number] = [
			ticket?.last_name || '',
			ticket?.first_name || '',
			ticket?.phone_number || ''
		];
	}

	let pager = $state({ idFrom: 0, idTo: 0 });
	let items = $state([]);
	let itemsLength = $derived(items.length || 1);
	let itemsBuffer = $derived(items.filter((i) => i.changed));
	const functions = {
		// Saves the marked rows, then loads the pager's range, or `range` when given.
		async getPage(range) {
			// Rows that could not be saved stay on the page, with the message why.
			if (!(await this.save())) return;
			if (range) [pager.idFrom, pager.idTo] = range;
			if (pager.idFrom > pager.idTo) {
				[pager.idFrom, pager.idTo] = [pager.idTo, pager.idFrom];
			}
			if (pager.idTo - pager.idFrom > 300) {
				pager.idTo = pager.idFrom + 300;
			}
			// Numbers start at 0: a row below it could not be saved.
			if (pager.idFrom < 0) pager.idFrom = 0;
			if (pager.idTo < 0) pager.idTo = 0;
			let resData;
			try {
				resData = await getJSON(
					`/api/drawing/${encodeURIComponent(prefix.prefix)}/${pager.idFrom}/${pager.idTo}`
				);
			} catch (e) {
				alert(`Error loading rows: ${errorMessage(e)}`);
				return;
			}
			resData.map((i) => (i.changed = false));
			items = startFrom(DRAWING_FORM, resData);
			setTimeout(() => focusIdx(0));
		},
		// Resolves to false when the marked rows could not be saved.
		async save(opts = {}) {
			const problem = await saveMarked('/api/drawing', itemsBuffer, {
				keepalive: !!opts.keepalive,
				// A drawing line stores its winning ticket; the winner's name
				// beside it shows the lookup of the winner now saved.
				form: DRAWING_FORM,
				after: showWinner
			});
			// A save made as the page is hidden or closed shows nothing and leaves
			// the cursor where it is: the volunteer may come back to the row.
			if (problem) {
				if (!opts.keepalive) alert(problem);
				return false;
			}
			if (!opts.keepalive) setTimeout(() => focusIdx(0), 1);
			return true;
		},
		cancel() {
			if (itemsBuffer.length > 0) {
				itemsBuffer.forEach((i) => (i.changed = false));
				this.getPage();
			}
		},
		pagerFromUpdate() {
			pager.idTo = pager.idFrom + (itemsLength - 1);
		},
		prevPage() {
			// Stops at 0, keeping the page's size: 1-10 goes to 0-9.
			const from = Math.max(0, pager.idFrom - itemsLength);
			this.getPage([from, from + (pager.idTo - pager.idFrom)]);
		},
		nextPage() {
			this.getPage([pager.idFrom + itemsLength, pager.idTo + itemsLength]);
		},
		nextLine() {
			if (items[nextIdx]) {
				setTimeout(() => {
					focusIdx(nextIdx);
				}, 1);
			} else {
				setTimeout(() => {
					focusIdx(curIdx);
				}, 1);
			}
		},
		prevLine() {
			if (curIdx > 0) {
				setTimeout(() => {
					focusIdx(prevIdx);
				}, 1);
			} else {
				setTimeout(() => {
					focusIdx(curIdx);
				}, 1);
			}
		},
		dupDown() {
			if (items[nextIdx]) {
				const buffer = rowValues(items[curIdx], 'b_id');
				items[nextIdx] = { ...items[nextIdx], ...buffer, changed: true };
				this.nextLine();
			} else {
				focusIdx(curIdx);
			}
		},
		dupUp() {
			if (curIdx > 0) {
				const buffer = rowValues(items[curIdx], 'b_id');
				items[prevIdx] = { ...items[prevIdx], ...buffer, changed: true };
				this.prevLine();
			} else {
				focusIdx(curIdx);
			}
		},
		copy() {
			if (items[curIdx]) {
				const buffer = rowValues(items[curIdx], 'b_id');
				window.localStorage.setItem('tam-drawing', JSON.stringify(buffer));
			}
			focusIdx(curIdx);
		},
		paste() {
			if (items[curIdx]) {
				const buffer = rowValues(JSON.parse(window.localStorage.getItem('tam-drawing')), 'b_id');
				items[curIdx] = { ...items[curIdx], ...buffer, changed: true };
			}
			focusIdx(curIdx);
		}
	};
	const headers = ['Basket ID', 'Description', 'Winning Ticket', 'Winner', 'Save?'];

	// Marked rows are saved when the page is hidden, left or closed.
	$effect(() => saveOnLeave(() => itemsBuffer, (opts) => functions.save(opts)));
</script>

<svelte:head>
	<title>{pageTitle}</title>
</svelte:head>

<table class="w-full box-border border-separate p-1">
	<thead class="sticky top-1 bg-white">
		<tr>
			<td colspan="50">
				<HeaderBar>
					<div>Drawing Forms:</div>
					{#each prefixes as p (p.prefix)}
						<a
							href={prefixPage('/drawing/[prefix]', p.prefix)}
							class={prefix.prefix == p.prefix ? bAS[p.color] : bS[p.color]}>{p.prefix}</a
						>
					{/each}
				</HeaderBar>
				<h1 class="text-xl font-bold p-1">{pageTitle}</h1>
				<PagerBar {prefix} {functions} bind:pager />
				<CommandBar {prefix} {functions} /></td
			>
		</tr>
		<tr>
			{#each headers as header (header)}
				<th class="border text-left p-0.5">{header}</th>
			{/each}
		</tr>
	</thead>
	<tbody>
		{#each items as item, idx (item.b_id)}
			<tr
				class="focus-within:font-bold {rBS[prefix.color]}"
				onfocusin={(e) => {
					changeIdx(idx);
					e.target.scrollIntoView({ block: 'center' });
				}}
			>
				<td class="p-0.5 border">{item.b_id}</td>
				<td class="p-0.5 border">{item.description}</td>
				<td class="p-0.5 border"
					><input
						type="number"
						class="{iS.normal} w-full"
						id="{idx}_first"
						aria-label="Basket {item.b_id} winning ticket"
						oninput={() => {
							item.changed = true;
							showWinner(item);
						}}
						bind:value={item.winning_ticket}
					/></td
				>
				<td class="p-0.5 border">
					{item.last_name || ''}, {item.first_name || ''}: {item.phone_number || ''}
				</td>
				<td class="p-0.5 border"
					><button
						class={bS[prefix.color]}
						tabindex="-1"
						onclick={() => {
							item.changed ? (item.changed = false) : (item.changed = true);
						}}>{item.changed ? 'Yes' : 'No'}</button
					></td
				>
			</tr>
		{:else}
			<tr>
				<td class="p-0.5 border text-center" colspan="50">
					No rows loaded. Please use the pager at the top to put in the first, then last number on
					the sheet, click Go, and that should load in the sheet.
				</td>
			</tr>
		{/each}
	</tbody>
</table>
