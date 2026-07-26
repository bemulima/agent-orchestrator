import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { StatusBadge } from "@/components/status-badge";

describe("StatusBadge", () => {
  it("renders a human label and semantic group", () => {
    render(<StatusBadge status="changes_requested" />);
    expect(screen.getByText("Нужны изменения")).toHaveClass("status-attention");
  });
});
