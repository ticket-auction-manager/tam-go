<script>
	import { prefixPage } from '$lib/client/paths';
	import { bS, bAS, iS, rBS } from '$lib/client/styles';
	import { getJSON, saveMarked, saveOnLeave, errorMessage, startFrom, rowValues, TICKET_FORM } from '$lib/client/api';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';
	import PagerBar from '$lib/client/components/PagerBar.svelte';
	import CommandBar from '$lib/client/components/CommandBar.svelte';

	let { data } = $props();
	let { prefix, prefixes, defaultPref } = $derived(data);

	let pageTitle = $derived(`${prefix.prefix} Tickets | TAM`);

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

	// The keys of the original's CALL/TEXT button, on the Pref select: C sets
	// CALL, T sets TEXT, Space and Enter switch between them. The select's own
	// handling of these keys is held back (its type-ahead kept "t" then "c"
	// on TEXT, and Space opened the list); Tab, the arrow keys, the mouse and
	// the Alt shortcuts work as usual.
	function prefKey(e, item) {
		if (e.altKey || e.ctrlKey || e.metaKey) return;
		let pref;
		if (e.key === 'c' || e.key === 'C') pref = 'CALL';
		else if (e.key === 't' || e.key === 'T') pref = 'TEXT';
		else if (e.key === ' ' || e.key === 'Enter') pref = item.pref === 'CALL' ? 'TEXT' : 'CALL';
		else return;
		e.preventDefault();
		if (item.pref !== pref) {
			item.pref = pref;
			item.changed = true;
		}
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
					`/api/tickets/${encodeURIComponent(prefix.prefix)}/${pager.idFrom}/${pager.idTo}`
				);
			} catch (e) {
				alert(`Error loading rows: ${errorMessage(e)}`);
				return;
			}
			resData.forEach((i) => {
				i.changed = false;
				// A new row (no data yet, no preference) takes the workstation default.
				if (!i.pref && !i.first_name && !i.last_name && !i.phone_number) i.pref = defaultPref;
				if (i.pref == null) i.pref = '';
			});
			items = startFrom(TICKET_FORM, resData);
			setTimeout(() => focusIdx(0));
		},
		pagerFromUpdate() {
			pager.idTo = pager.idFrom + (itemsLength - 1);
		},
		// Resolves to false when the marked rows could not be saved.
		async save(opts = {}) {
			const problem = await saveMarked('/api/tickets', itemsBuffer, {
				keepalive: !!opts.keepalive,
				form: TICKET_FORM
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
				const buffer = rowValues(items[curIdx], 't_id');
				items[nextIdx] = { ...items[nextIdx], ...buffer, changed: true };
				this.nextLine();
			} else {
				focusIdx(curIdx);
			}
		},
		dupUp() {
			if (curIdx > 0) {
				const buffer = rowValues(items[curIdx], 't_id');
				items[prevIdx] = { ...items[prevIdx], ...buffer, changed: true };
				functions.prevLine();
			} else {
				focusIdx(curIdx);
			}
		},
		copy() {
			if (items[curIdx]) {
				const buffer = rowValues(items[curIdx], 't_id');
				window.localStorage.setItem('tam-ticket', JSON.stringify(buffer));
			}
			setTimeout(() => focusIdx(curIdx), 1);
		},
		paste() {
			if (items[curIdx]) {
				const buffer = rowValues(JSON.parse(window.localStorage.getItem('tam-ticket')), 't_id');
				items[curIdx] = { ...items[curIdx], ...buffer, changed: true };
			}
			setTimeout(() => focusIdx(curIdx), 1);
		}
	};
	const headers = ['Ticket ID', 'First Name', 'Last Name', 'Phone Number', 'Pref', 'Save?'];

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
					<div>Tickets:</div>
					{#each prefixes as p (p.prefix)}
						<a
							href={prefixPage('/tickets/[prefix]', p.prefix)}
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
		{#each items as item, idx (item.t_id)}
			<tr
				class="focus-within:font-bold {rBS[prefix.color]}"
				onfocusin={(e) => {
					changeIdx(idx);
					e.target.scrollIntoView({ block: 'center' });
				}}
			>
				<td class="p-0.5 border">{item.t_id}</td>
				<td class="p-0.5 border"
					><input
						type="text"
						class="{iS.normal} w-full"
						id="{idx}_first"
						aria-label="Ticket {item.t_id} first name"
						oninput={() => (item.changed = true)}
						bind:value={item.first_name}
					/></td
				>
				<td class="p-0.5 border"
					><input
						type="text"
						class="{iS.normal} w-full"
						id="{idx}_second"
						aria-label="Ticket {item.t_id} last name"
						oninput={() => (item.changed = true)}
						bind:value={item.last_name}
					/></td
				>
				<td class="p-0.5 border"
					><input
						type="text"
						class="{iS.normal} w-full"
						id="{idx}_third"
						aria-label="Ticket {item.t_id} phone number"
						oninput={() => (item.changed = true)}
						bind:value={item.phone_number}
					/></td
				>
				<td class="p-0.5 border"
					><select
						class="{iS.normal} w-full"
						id="{idx}_fourth"
						aria-label="Ticket {item.t_id} contact preference"
						onkeydown={(e) => prefKey(e, item)}
						onchange={() => (item.changed = true)}
						bind:value={item.pref}
					>
						{#if item.pref !== 'CALL' && item.pref !== 'TEXT'}
							<!-- A value from the database that is neither CALL nor TEXT stays as it is. -->
							<option value={item.pref}>{item.pref || '(blank)'}</option>
						{/if}
						<option value="CALL">CALL</option>
						<option value="TEXT">TEXT</option>
					</select></td
				>
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
