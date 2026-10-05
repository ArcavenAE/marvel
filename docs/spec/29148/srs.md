---
standard: ISO/IEC/IEEE 29148:2018
document_type: Software Requirements Specification (SRS)
description: Specifies software requirements derived from system requirements
---

# Software Requirements Specification

<!-- Replace [SYSTEM NAME] throughout this document -->

## 1. Introduction

### 1.1 Purpose

<!-- Define the purpose of this SRS. Identify the software product(s) to be
     produced by name. Specify the version/release number. -->

[TODO: purpose]

### 1.2 Scope

<!-- Identify the software product(s) by name. Explain what the software will
     and will not do. Describe the application, including benefits, objectives,
     and goals. Be consistent with the higher-level system spec if one exists. -->

[TODO: scope]

### 1.3 Intended Audience

<!-- List the different types of reader the document is intended for:
     developers, project managers, testers, users, maintainers. Suggest a
     reading order if sections vary by audience. -->

[TODO: audience]

### 1.4 References

<!-- List all documents and sources referenced. Provide title, report number,
     date, and source for each. -->

[TODO: references]

### 1.5 Definitions and Acronyms

<!-- Define all terms, abbreviations, and acronyms required to properly
     interpret this SRS. -->

| Term | Definition |
|------|-----------|
| [TODO] | [TODO] |

### 1.6 Document Overview

<!-- Describe what the rest of this SRS contains and how it is organized. -->

[TODO: overview]

## 2. Overall Description

### 2.1 System Perspective

<!-- Describe the context and origin of the software. Is it a replacement? A
     new product? A component of a larger system? If part of a larger system,
     describe the interfaces between this software and the system. Use a block
     diagram showing major components and their interconnections. -->

[TODO: perspective]

### 2.2 Product Functions

<!-- Summarize the major functions the software will perform. Organize by how
     the user will perceive them, not by internal design. Use a high-level
     summary (detailed functions go in Section 3). -->

[TODO: functions]

### 2.3 User Characteristics

<!-- Describe the general characteristics of the intended users: education
     level, experience, technical expertise. -->

[TODO: users]

### 2.4 Constraints

<!-- General description of any items that will limit the developer's options:
     regulatory policies, hardware limitations, audit functions, reliability
     requirements, criticality of the application, safety and security
     considerations. -->

[TODO: constraints]

### 2.5 Assumptions and Dependencies

<!-- List assumptions that, if changed, would affect requirements. Example:
     "assumes a specific operating system is available." List dependencies on
     third-party components, external systems, or data sources. -->

[TODO: assumptions]

## 3. Functional Requirements

<!-- Organize by feature, user class, use case, or stimulus — choose one
     organizational scheme and apply consistently.

     Each requirement shall be:
     - Necessary: traceable to a stakeholder need
     - Unambiguous: one interpretation only
     - Verifiable: a test or inspection can confirm it
     - Singular: one requirement per statement
     - Feasible: implementable within known constraints

     Use "shall" for mandatory requirements, "should" for desirable. -->

### 3.1 [Feature/Use Case Name]

<!-- Repeat this subsection for each feature or use case -->

**Description:** [TODO: what this feature does]

**Priority:** [High | Medium | Low]

**Stimulus/Response:**

| Stimulus | Response |
|----------|----------|
| [TODO] | [TODO] |

**Requirements:**

- **REQ-FUNC-001:** The system shall [TODO].
- **REQ-FUNC-002:** The system shall [TODO].

## 4. External Interface Requirements

### 4.1 User Interfaces

<!-- Describe logical characteristics of each interface between the software
     and the users. Screen layouts, GUI standards, shortcut keys, error
     messages. Reference any UI style guide. -->

[TODO: user interfaces]

### 4.2 Hardware Interfaces

<!-- Describe logical and physical characteristics of each interface between
     the software and hardware components. Supported device types, data and
     control interactions, communication protocols. -->

[TODO: hardware interfaces]

### 4.3 Software Interfaces

<!-- Specify use of other required software products. For each: name, version,
     source, purpose. Describe data items or messages coming into and going
     out of the system. -->

[TODO: software interfaces]

### 4.4 Communications Interfaces

<!-- Describe requirements associated with any communications functions:
     e-mail, web browser, network protocols, electronic forms. Define message
     formatting, communication security, data transfer rates. -->

[TODO: communications interfaces]

## 5. Non-Functional Requirements

### 5.1 Performance Requirements

<!-- Specify static and dynamic numerical requirements. Static: number of
     simultaneous users, amount of data. Dynamic: transactions per second,
     response time. -->

- **REQ-PERF-001:** [TODO]

### 5.2 Safety Requirements

<!-- Specify requirements to prevent damage, injury, or loss. Include
     safeguards, actions to prevent or mitigate hazards. -->

[TODO: safety]

### 5.3 Security Requirements

<!-- Specify requirements regarding security or privacy issues surrounding
     use of the product or protection of the data. Access limitations,
     authentication, data integrity, privacy constraints. -->

- **REQ-SEC-001:** [TODO]

### 5.4 Reliability and Availability

<!-- Specify factors required to establish required reliability and
     availability. MTBF, MTTR, acceptable failure rates, recovery time. -->

[TODO: reliability]

### 5.5 Usability

<!-- Specify usability requirements: learnability, efficiency, memorability,
     error rates, satisfaction. Reference any accessibility standards. -->

[TODO: usability]

### 5.6 Maintainability

<!-- Specify attributes of the software that relate to ease of maintenance:
     modularity, analyzability, changeability, testability. -->

[TODO: maintainability]

### 5.7 Portability

<!-- Specify attributes related to portability: adaptability, installability,
     replaceability, co-existence. -->

[TODO: portability]

## 6. Other Requirements

<!-- Requirements not covered above: legal, regulatory, compliance,
     internationalization, data migration, packaging, installation. -->

[TODO: other requirements or "None"]

## 7. Verification and Validation

<!-- For each requirement or requirement group, define how it will be
     verified. Methods: inspection, analysis, demonstration, test. -->

### 7.1 Verification Methods

| Requirement | Method | Description |
|------------|--------|-------------|
| REQ-FUNC-001 | [Test/Inspection/Analysis/Demo] | [TODO] |

### 7.2 Acceptance Criteria

<!-- Define the criteria that must be met for stakeholder acceptance. -->

[TODO: acceptance criteria]

## 8. Appendices

### 8.1 Glossary

<!-- Expanded definitions if Section 1.5 is insufficient. -->

### 8.2 Traceability Matrix

<!-- Bidirectional trace: stakeholder need → system requirement → software
     requirement → verification method. -->

| Stakeholder Need | System Req | Software Req | Verification |
|-----------------|-----------|-------------|-------------|
| [TODO] | [TODO] | [TODO] | [TODO] |

### 8.3 Supporting Diagrams

<!-- Use case diagrams, data flow diagrams, entity relationship diagrams,
     or other models that support the requirements. -->
