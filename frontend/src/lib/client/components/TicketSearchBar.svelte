<script>
  import { bS, iS } from "../styles";
  import { altShortcuts } from "../shortcuts";

  let { prefix, functions, searchForm = $bindable() } = $props();

  $effect(() =>
    altShortcuts({
      q: () => document.getElementById("search_first_name")?.select(),
      w: () => document.getElementById("search_last_name")?.select(),
      e: () => document.getElementById("search_phone_number")?.select(),
    }),
  );

  // Enter in any of the boxes runs the search, as the Search button does.
  const searchOnEnter = (e) => {
    if (e.key === "Enter" && functions.search) {
      e.preventDefault();
      functions.search();
    }
  };
</script>

<div class="flex flex-row justify-between gap-1 p-1">
  <div class="flex flex-row gap-1 items-center">
    <div>First Name:</div>
    <input
      type="text"
      id="search_first_name"
      class={iS.normal}
      bind:value={searchForm.first_name}
      onclick={(e) => e.target.select()}
      onkeydown={searchOnEnter}
    />
    <div>Last Name:</div>
    <input
      type="text"
      id="search_last_name"
      class={iS.normal}
      bind:value={searchForm.last_name}
      onclick={(e) => e.target.select()}
      onkeydown={searchOnEnter}
    />
    <div>Phone Number:</div>
    <input
      type="text"
      id="search_phone_number"
      class={iS.normal}
      bind:value={searchForm.phone_number}
      onclick={(e) => e.target.select()}
      onkeydown={searchOnEnter}
    />
    {#if functions.search}
      <button class={bS[prefix.color]} onclick={() => functions.search()}
        >Search</button
      >
    {/if}
  </div>
</div>
