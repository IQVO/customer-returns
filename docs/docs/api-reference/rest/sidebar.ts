import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/customer-returns-api",
    },
    {
      type: "category",
      label: "Return Authorizations",
      link: {
        type: "doc",
        id: "api-reference/rest/return-authorizations",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/authorize-return",
          label: "Authorize a return (AuthorizeReturn)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/list-return-authorizations",
          label: "List an order's return authorizations (ListReturnAuthorizations)",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-return-authorization",
          label: "Read one return authorization (GetReturnAuthorization)",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/cancel-return",
          label: "Cancel a return (CancelReturn)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/issue-return-label",
          label: "Issue the shopper's reverse label (IssueLabel)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/healthz",
          label: "Liveness",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/readyz",
          label: "Readiness (drain gate)",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "Reports",
      link: {
        type: "doc",
        id: "api-reference/rest/reports",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-returns-performance-report",
          label: "Returns performance per UTC day, reason Pareto and freshness",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-report-freshness",
          label: "How far the analytics projection is behind",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
