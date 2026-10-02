<script>
	import { untrack } from 'svelte';
	import HeaderBar from '$lib/client/components/HeaderBar.svelte';
	import { bS, bAS, iS, tS } from '$lib/client/styles';
	import { postJSON, readDetail } from '$lib/client/api';
	import { resolve } from '$app/paths';

	let { data } = $props();

	const pageTitle = 'Prefixes | TAM';

	// Local copy of the loaded prefixes (intentionally captured once; the page reloads after changes).
	let prefixes = $state(untrack(() => [...data.prefixes]));
	let editPrefix = $state({ prefix: '', color: 'white', weight: 1 });
	let status = $state('');

	// Back to the prefix name when a name is refused. After a save the page
	// reloads, and the name field's autofocus puts the cursor there instead:
	// SvelteKit sets the focus itself once a page has loaded, on the element
	// with autofocus or else on the page, so focusing earlier does not hold.
	const selectPrefixInput = () => {
		const form_prefix = document.getElementById('form_prefix');
		if (form_prefix) {
			form_prefix.focus();
			form_prefix.select();
		}
	};

	async function addChange() {
		const name = String(editPrefix.prefix ?? '').trim();
		if (!name) {
			status = 'Prefix name cannot be empty.';
			selectPrefixInput();
			return;
		}
		const row = { prefix: name, color: editPrefix.color, weight: Math.trunc(Number(editPrefix.weight) || 0) };
		// A prefix opened with Edit goes with the values it had then: a colour
		// or weight another computer changed meanwhile is not overwritten.
		const base = editPrefix.from === name ? editPrefix.base : undefined;
		let res;
		try {
			res = await postJSON('/api/prefixes', [base ? { ...row, base } : row]);
		} catch {
			status = 'Could not reach the TAM client API';
			return;
		}
		if (!res.ok) {
			status = await readDetail(res);
			return;
		}
		if (base) {
			let now = null;
			try {
				[now] = await res.json();
			} catch {
				// No row in the answer.
			}
			const kept = now
				? [
						['color', 'color'],
						['weight', 'weight']
					].filter(([f]) => String(row[f]) !== String(base[f]) && String(now[f]) !== String(row[f]))
				: [];
			if (kept.length > 0) {
				alert(
					`Not changed: another computer changed the ${kept.map(([, label]) => label).join(' and ')} of ${name} after you opened it (now ${kept.map(([f]) => now[f]).join(', ')}). Edit it again to change it.`
				);
			}
		}
		window.location.reload();
	}

	async function deletePrefix(prefix) {
		let res;
		try {
			res = await fetch(`/api/prefixes?p=${encodeURIComponent(prefix.prefix)}`, {
				method: 'DELETE'
			});
		} catch {
			status = 'Could not reach the TAM client API';
			return;
		}
		if (res.ok) {
			window.location.reload();
		} else {
			status = await readDetail(res);
		}
	}
</script>

<svelte:head>
	<title>{pageTitle}</title>
</svelte:head>

<div id="app_container" class="p-1">
	<HeaderBar>
		<a href={resolve('/settings')} class={bS.gray}>Back to Settings</a>
	</HeaderBar>
	<h1 class="text-xl font-bold">{pageTitle}</h1>
	<div class="flex flex-row gap-1 py-1 items-center">
		<div class="flex flex-col gap-1">
			<div>Prefix</div>
			<!-- The cursor belongs in the first field of this form; see selectPrefixInput. -->
			<!-- svelte-ignore a11y_autofocus -->
			<input type="text" id="form_prefix" class={iS.normal} bind:value={editPrefix.prefix} autofocus />
		</div>
		<div class="flex flex-col gap-1">
			<div>Color</div>
			<select id="form_color" class={iS.normal} bind:value={editPrefix.color}>
				<option value="white">White</option>
				<option value="blue">Blue</option>
				<option value="yellow">Yellow</option>
				<option value="green">Green</option>
				<option value="orange">Orange</option>
				<option value="purple">Purple</option>
				<option value="red">Red</option>
			</select>
		</div>
		<div class="flex flex-col gap-1">
			<div>Weight</div>
			<input
				type="number"
				id="form_weight"
				step="1"
				min="0"
				class={iS.normal}
				bind:value={editPrefix.weight}
			/>
		</div>
		<div class="flex flex-col gap-1">
			<div>Actions</div>
			<button class={bS[editPrefix.color]} onclick={addChange}>Add/Change</button>
		</div>
	</div>
	{#if status}
		<div class="py-1">
			<p class={tS.red}>{status}</p>
		</div>
	{/if}
	<div class="flex flex-row gap-1 py-1 items-center">
		<div>Colors:</div>
		<button
			class={editPrefix.color == 'white' ? bAS.white : bS.white}
			onclick={() => (editPrefix.color = 'white')}>White</button
		>
		<button
			class={editPrefix.color == 'blue' ? bAS.blue : bS.blue}
			onclick={() => (editPrefix.color = 'blue')}>Blue</button
		>
		<button
			class={editPrefix.color == 'yellow' ? bAS.yellow : bS.yellow}
			onclick={() => (editPrefix.color = 'yellow')}>Yellow</button
		>
		<button
			class={editPrefix.color == 'green' ? bAS.green : bS.green}
			onclick={() => (editPrefix.color = 'green')}>Green</button
		>
		<button
			class={editPrefix.color == 'orange' ? bAS.orange : bS.orange}
			onclick={() => (editPrefix.color = 'orange')}>Orange</button
		>
		<button
			class={editPrefix.color == 'purple' ? bAS.purple : bS.purple}
			onclick={() => (editPrefix.color = 'purple')}>Purple</button
		>
		<button
			class={editPrefix.color == 'red' ? bAS.red : bS.red}
			onclick={() => (editPrefix.color = 'red')}>Red</button
		>
	</div>
	<table class="w-full border-separate">
		<thead class="text-left">
			<tr>
				<th class="border p-0.5">Prefix</th>
				<th class="border p-0.5">Color</th>
				<th class="border p-0.5">Weight</th>
				<th class="border p-0.5">Actions</th>
			</tr>
		</thead>
		<tbody>
			{#each prefixes as prefix (prefix.prefix)}
				<tr>
					<td class="border p-0.5">{prefix.prefix}</td>
					<td class="border p-0.5"
						>{prefix.color.charAt(0).toUpperCase() + prefix.color.slice(1)}</td
					>
					<td class="border p-0.5">{prefix.weight}</td>
					<td class="border p-0.5">
						<div class="flex flex-row gap-1 items-center">
							<button
								class={bS[prefix.color]}
								onclick={() =>
									(editPrefix = {
										...prefix,
										from: prefix.prefix,
										base: { color: prefix.color, weight: prefix.weight }
									})}>Edit</button
							>
							<button class={bS[prefix.color]} onclick={() => deletePrefix(prefix)}>Delete</button>
						</div>
					</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>
