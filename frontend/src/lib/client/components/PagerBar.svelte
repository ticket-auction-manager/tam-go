<script>
	import { bS, iS } from '../styles';
	import { altShortcuts } from '../shortcuts';

	let { prefix, functions, pager = $bindable() } = $props();

	$effect(() =>
		altShortcuts({
			q: () => document.getElementById('id_from')?.select(),
			w: () => document.getElementById('id_to')?.select(),
			b: () => functions.prevPage?.(),
			n: () => functions.nextPage?.()
		})
	);
</script>

<div class="flex flex-row justify-between gap-1 p-1">
	<div class="flex flex-row gap-1">
		<input
			type="number"
			id="id_from"
			class={iS.normal}
			title="Alt + Q"
			onclick={(e) => {
				e.target.select();
			}}
			oninput={() => {
				if (functions.pagerFromUpdate) functions.pagerFromUpdate();
			}}
			onkeydown={(e) => {
				if (e.key == 'Enter') {
					functions.getPage();
				}
			}}
			bind:value={pager.idFrom}
		/>
		<div>-</div>
		<input
			type="number"
			id="id_to"
			class={iS.normal}
			title="Alt + W"
			onclick={(e) => {
				e.target.select();
			}}
			onkeydown={(e) => {
				if (e.key == 'Enter') {
					functions.getPage();
				}
			}}
			bind:value={pager.idTo}
		/>
		<button
			class={bS[prefix.color]}
			onclick={() => {
				functions.getPage();
			}}>Go</button
		>
	</div>
	<div class="flex flex-row gap-1">
		{#if functions.prevPage}
			<button
				class={bS[prefix.color]}
				title="Alt + B"
				onclick={() => {
					functions.prevPage();
				}}>Prev Page</button
			>
		{/if}
		{#if functions.nextPage}
			<button
				class={bS[prefix.color]}
				title="Alt + N"
				onclick={() => {
					functions.nextPage();
				}}>Next Page</button
			>
		{/if}
	</div>
</div>
