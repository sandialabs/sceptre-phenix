// A mixin for Buefy's modals, dialogs and dropdowns, which leave focus on the
// page's body when they close: it returns focus to what had it on opening.
export const returnFocus = {
  created() {
    if (this.isActive) this.focusOpener = document.activeElement;
  },
  watch: {
    isActive(active) {
      if (active) {
        this.focusOpener = document.activeElement;
        return;
      }
      const opener = this.focusOpener;
      this.focusOpener = null;
      this.$nextTick(() => {
        const now = document.activeElement;
        // only when focus was lost with it, not moved on by the user or a page
        const lost = !now || now === document.body || this.$el?.contains?.(now);
        if (lost && opener?.isConnected && opener !== document.body) {
          opener.focus();
        }
      });
    },
  },
};
