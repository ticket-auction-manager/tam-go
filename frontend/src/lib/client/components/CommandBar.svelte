<script>
	import { bS } from '../styles';
	import { altShortcuts } from '../shortcuts';

	let { prefix, functions } = $props();

	$effect(() =>
		altShortcuts({
			l: () => functions.nextLine?.(),
			o: () => functions.prevLine?.(),
			j: () => functions.dupDown?.(),
			u: () => functions.dupUp?.(),
			c: () => functions.copy?.(),
			v: () => functions.paste?.(),
			s: () => functions.save?.()
		})
	);
</script>

<div class="flex flex-row justify-between gap-1 py-1">
	<div class="flex flex-row gap-1">
		{#if functions.dupDown}
			<button class={bS[prefix.color]} title="Alt + J" onclick={() => functions.dupDown()}
				>Duplicate Down</button
			>
		{/if}
		{#if functions.dupUp}
			<button class={bS[prefix.color]} title="Alt + U" onclick={() => functions.dupUp()}
				>Duplicate Up</button
			>
		{/if}
		{#if functions.nextLine}
			<button class={bS[prefix.color]} title="Alt + L" onclick={() => functions.nextLine()}
				>Next Line</button
			>
		{/if}
		{#if functions.prevLine}
			<button class={bS[prefix.color]} title="Alt + O" onclick={() => functions.prevLine()}
				>Previous Line</button
			>
		{/if}
		{#if functions.copy}
			<button class={bS[prefix.color]} title="Alt + C" onclick={() => functions.copy()}>Copy</button
			>
		{/if}
		{#if functions.paste}
			<button class={bS[prefix.color]} title="Alt + V" onclick={() => functions.paste()}
				>Paste</button
			>
		{/if}
	</div>
	<div class="flex flex-row gap-1">
		{#if functions.save}
			<button class={bS[prefix.color]} title="Alt + S" onclick={() => functions.save()}
				>Save Marked</button
			>
		{/if}
		{#if functions.cancel}
			<!-- No shortcut: Alt + S is Save Marked. -->
			<button
				class={bS[prefix.color]}
				onclick={() => {
					const yes_no = confirm(
						'This will cancel all pending changes. Are you sure you want to cancel?'
					);
					if (yes_no) {
						functions.cancel();
					}
				}}>Cancel Marked</button
			>
		{/if}
	</div>
</div>
